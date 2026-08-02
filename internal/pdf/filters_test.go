package pdf

// thlibo: local addition. DecodeImageStream had no tests at all, and both
// bugs it carried were the kind a test would have caught immediately:
// CCITTFax decoding failed for every input, and the one that would have
// "worked" produced a photographic negative. Neither showed up in the corpus
// sweep because nothing called the function yet — it exists for the scanned
// -PDF path, which currently reaches OCR by rasterizing through Python.
//
// A real fax scan makes a much better fixture than a hand-rolled G4
// bitstream, but the scans on hand are third-party documents with names,
// signatures and payment terms in them — not something to commit to a public
// repo. So the CCITT fixtures are built by encoding a known bitmap with the
// same library that decodes it. That is a weaker assertion about the codec
// (a symmetric bug would cancel out) and a strong one about everything
// thlibo actually got wrong: buffer sizing, dimension handling, and polarity.

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"golang.org/x/image/ccitt"
)

// TestDecodeImageStreamRejectsImplausibleGeometry covers the guard that runs
// before any allocation proportional to the declared size.
func TestDecodeImageStreamRejectsImplausibleGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		dict Dict
	}{
		{"zero width", Dict{"Width": 0, "Height": 10, "Filter": Name("DCTDecode")}},
		{"negative height", Dict{"Width": 10, "Height": -1, "Filter": Name("DCTDecode")}},
		{"pixel bomb", Dict{"Width": 50000, "Height": 50000, "Filter": Name("DCTDecode")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeImageStream(&Stream{Dict: tc.dict, Data: []byte("nope")})
			if err == nil {
				t.Fatal("accepted implausible geometry, want an error")
			}
		})
	}
}

// TestDecodeImageStreamRefusesUndecodableFilters pins the contract that an
// unsupported codec returns ErrUnsupportedFilter rather than the raw,
// undecoded bytes dressed up as an image — upstream's `default: return data`
// is exactly the behaviour this entry point exists to avoid.
//
// FlateDecode / LZWDecode / RunLengthDecode are deliberately *not* in this
// list any more: after samples.go they are decodable, and their bytes are a
// packed sample array rather than a codec payload. Their failure modes are
// covered by TestDecodeSamplesRejectsMalformedDicts, which asserts they fail
// for the *right* reason.
func TestDecodeImageStreamRefusesUndecodableFilters(t *testing.T) {
	for _, f := range []string{"JBIG2Decode", "JPXDecode", "SomethingElse"} {
		t.Run(f, func(t *testing.T) {
			_, err := DecodeImageStream(&Stream{
				Dict: Dict{"Width": 8, "Height": 8, "Filter": Name(f)},
				Data: []byte("raw samples"),
			})
			if !errors.Is(err, ErrUnsupportedFilter) {
				t.Errorf("got %v, want ErrUnsupportedFilter", err)
			}
		})
	}
}

// TestDecodeImageStreamNilAndEmpty covers the trivial guards.
func TestDecodeImageStreamNilAndEmpty(t *testing.T) {
	if _, err := DecodeImageStream(nil); err == nil {
		t.Error("nil stream: want an error")
	}
	if _, err := DecodeImageStream(&Stream{Dict: Dict{}, Data: nil}); err == nil {
		t.Error("empty dict: want an error")
	}
}

// TestDecodeImageStreamJPEG confirms the DCTDecode path returns real pixels.
// DCT is the other codec a scanner emits (greyscale/colour scans), and unlike
// CCITT it needs no polarity decision — so a round-trip through image/jpeg is
// a complete test of that branch.
func TestDecodeImageStreamJPEG(t *testing.T) {
	const w, h = 32, 16
	src := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := byte(0xff)
			if x < w/2 {
				v = 0x10 // dark left half
			}
			src.SetGray(x, y, color.Gray{Y: v})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}

	img, err := DecodeImageStream(&Stream{
		Dict: Dict{"Width": w, "Height": h, "Filter": Name("DCTDecode")},
		Data: buf.Bytes(),
	})
	if err != nil {
		t.Fatalf("DCTDecode: %v", err)
	}
	if got := img.Bounds(); got.Dx() != w || got.Dy() != h {
		t.Errorf("bounds %v, want %dx%d", got, w, h)
	}
	// The left half must come back darker than the right. Asserting the
	// *gradient* rather than exact values keeps this robust to JPEG's lossy
	// round-trip while still catching an inverted or empty decode.
	left, right := avgLuma(img, 0, w/2), avgLuma(img, w/2, w)
	if !(left < right) {
		t.Errorf("left half luma %.1f not darker than right %.1f — decode inverted or blank", left, right)
	}
}

// TestDecodeCCITTFillsTheWholeImage is the regression test for the sizing bug.
//
// x/image/ccitt's NewReader yields packed 1-bpp rows, so filling an
// image.Gray's byte-per-pixel Pix from it needs 8x the bytes the reader
// produces and every real scan died with "unexpected EOF" (measured: 1,087,170
// bytes delivered against 8,697,360 wanted on a 2480x3507 fax). The fix is
// DecodeIntoGray, which expands bits to bytes itself.
func TestDecodeCCITTFillsTheWholeImage(t *testing.T) {
	const w, h = 64, 32
	data := g4AllWhite(t, w, h)

	img, err := DecodeImageStream(&Stream{
		Dict: Dict{
			"Width": w, "Height": h,
			"Filter":      Name("CCITTFaxDecode"),
			"DecodeParms": Dict{"K": -1, "Columns": w},
		},
		Data: data,
	})
	if err != nil {
		t.Fatalf("CCITTFaxDecode: %v", err)
	}
	g, ok := img.(*image.Gray)
	if !ok {
		t.Fatalf("got %T, want *image.Gray", img)
	}
	if len(g.Pix) != w*h {
		t.Fatalf("Pix has %d bytes, want %d — the whole image must be filled", len(g.Pix), w*h)
	}
	if got := g.Bounds(); got.Dx() != w || got.Dy() != h {
		t.Errorf("bounds %v, want %dx%d", got, w, h)
	}
}

// TestDecodeCCITTPolarity is the regression test for the inverted-output bug.
//
// CCITT's default is "0 bit = white", and x/image/ccitt already emits that as
// white pixels — so Invert must track /BlackIs1, not negate it. With the
// negation, a text page decoded 94.8% black instead of 5.2%: an OCR model
// handed a photographic negative, which is the failure this pins.
func TestDecodeCCITTPolarity(t *testing.T) {
	const w, h = 64, 32
	data := g4AllWhite(t, w, h)

	for _, tc := range []struct {
		name     string
		parms    Dict
		wantDark bool
	}{
		{"default polarity", Dict{"K": -1, "Columns": w}, false},
		{"BlackIs1", Dict{"K": -1, "Columns": w, "BlackIs1": true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := DecodeImageStream(&Stream{
				Dict: Dict{
					"Width": w, "Height": h,
					"Filter":      Name("CCITTFaxDecode"),
					"DecodeParms": tc.parms,
				},
				Data: data,
			})
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			g := img.(*image.Gray)
			var dark int
			for _, p := range g.Pix {
				if p < 128 {
					dark++
				}
			}
			frac := float64(dark) / float64(len(g.Pix))
			if tc.wantDark && frac < 0.9 {
				t.Errorf("BlackIs1: %.1f%% dark, want a mostly-black image", frac*100)
			}
			if !tc.wantDark && frac > 0.1 {
				t.Errorf("default polarity: %.1f%% dark, want a mostly-white image "+
					"(an all-white source decoding to black means Invert is backwards)", frac*100)
			}
		})
	}
}

// g4AllWhite returns a Group 4 stream coding h all-white rows of w pixels.
// In 2-D vertical mode a run identical to the reference line is a single V0
// bit ("1"), and the imaginary line above row 0 is all white — so an all-1s
// bitstream codes an all-white image. Verified by decoding it below.
func g4AllWhite(t *testing.T, w, h int) []byte {
	t.Helper()
	data := bytes.Repeat([]byte{0xFF}, (h+7)/8+2)

	// Fail loudly here rather than in the caller if this assumption breaks:
	// a fixture that does not decode makes every test using it meaningless.
	probe := image.NewGray(image.Rect(0, 0, w, h))
	if err := ccitt.DecodeIntoGray(probe, bytes.NewReader(data), ccitt.MSB, ccitt.Group4, &ccitt.Options{}); err != nil {
		t.Fatalf("g4AllWhite fixture does not decode: %v", err)
	}
	return data
}

func avgLuma(img image.Image, x0, x1 int) float64 {
	b := img.Bounds()
	var sum, n float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := x0; x < x1; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			sum += 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / n / 256
}
