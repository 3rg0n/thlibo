package pdf

// thlibo: local addition. Tests for raw-sample image decoding.
//
// The bugs this file is built to catch are the ones that produce a
// *plausible* image rather than an error, because those are the ones that
// reach OCR and silently degrade it: row padding ignored (the image shears
// diagonally), sub-byte samples shifted instead of scaled (white comes back
// grey), /Decode inversion missed (a photographic negative), an Indexed
// palette index normalised as if it were a colour value (wrong colours
// entirely). Each has a test that fails loudly on the wrong answer, not
// merely on an error.

import (
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
)

// TestBytesPerRowPadsToByteBoundary is the row-stride test.
//
// PDF pads every row to a byte boundary, so a 9-pixel-wide 1-bpc image uses
// 2 bytes per row and the tenth bit of the buffer is the *second* row's
// first pixel, not the first row's tenth. Ignoring that shears the image
// diagonally by one bit per row — subtle enough to read as a corrupt source
// file rather than a decoder bug.
func TestBytesPerRowPadsToByteBoundary(t *testing.T) {
	for _, tc := range []struct {
		comps, bpc, width, want int
	}{
		{1, 1, 8, 1},
		{1, 1, 9, 2},  // the padding case
		{1, 1, 1, 1},  // a single pixel still costs a byte
		{1, 4, 3, 2},  // 12 bits -> 2 bytes
		{3, 8, 4, 12}, // RGB, no padding needed
		{1, 2, 5, 2},  // 10 bits -> 2 bytes
		{4, 8, 2, 8},  // CMYK
		{1, 16, 3, 6},
	} {
		f := sampleFormat{comps: tc.comps, bpc: tc.bpc}
		if got := f.bytesPerRow(tc.width); got != tc.want {
			t.Errorf("comps=%d bpc=%d width=%d: stride %d, want %d",
				tc.comps, tc.bpc, tc.width, got, tc.want)
		}
	}
}

// TestSampleAtScalesSubByteDepths pins the normalisation.
//
// A 4-bit value of 15 must come back as 255, not 240: shifting into place
// instead of scaling makes pure white slightly grey, which moves every
// downstream threshold and shows up as washed-out OCR input.
func TestSampleAtScalesSubByteDepths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   []byte
		index int
		bpc   int
		want  byte
	}{
		{"1bpc first pixel set", []byte{0x80}, 0, 1, 255},
		{"1bpc second pixel clear", []byte{0x80}, 1, 1, 0},
		{"1bpc last pixel in byte", []byte{0x01}, 7, 1, 255},
		{"2bpc max scales to 255", []byte{0xC0}, 0, 2, 255},
		{"2bpc mid", []byte{0x40}, 0, 2, 85}, // 1/3 of 255
		{"4bpc max scales to 255", []byte{0xF0}, 0, 4, 255},
		{"4bpc low nibble", []byte{0x0F}, 1, 4, 255},
		{"4bpc half", []byte{0x80}, 0, 4, 136}, // 8*255/15
		{"8bpc passthrough", []byte{0x7F}, 0, 8, 0x7F},
		{"16bpc takes high byte", []byte{0xAB, 0xCD}, 0, 16, 0xAB},
		{"out of range reads zero", []byte{0xFF}, 99, 8, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sampleAt(tc.row, tc.index, tc.bpc); got != tc.want {
				t.Errorf("sampleAt(%v, %d, %d) = %d, want %d", tc.row, tc.index, tc.bpc, got, tc.want)
			}
		})
	}
}

// TestRawSampleAtDoesNotScale is the counterpart: an Indexed image's samples
// are palette indices, so normalising them to 0..255 looks up the wrong
// colour entirely. Index 1 of a 4-bpc image must stay 1, not become 17.
func TestRawSampleAtDoesNotScale(t *testing.T) {
	if got := rawSampleAt([]byte{0x10}, 0, 4); got != 1 {
		t.Errorf("4bpc index = %d, want 1 (raw, not scaled)", got)
	}
	if got := rawSampleAt([]byte{0x80}, 0, 1); got != 1 {
		t.Errorf("1bpc index = %d, want 1", got)
	}
	if got := rawSampleAt([]byte{0x01, 0x02}, 0, 16); got != 0x0102 {
		t.Errorf("16bpc index = %d, want 258 (both bytes)", got)
	}
}

// TestDecodeSamplesGray decodes an 8-bpc DeviceGray gradient and checks the
// pixels land where they should, including across the row padding.
func TestDecodeSamplesGray(t *testing.T) {
	const w, h = 3, 2
	data := []byte{
		0x00, 0x80, 0xFF,
		0xFF, 0x80, 0x00,
	}
	img, err := decodeImage(&Stream{
		Dict: Dict{"Width": w, "Height": h, "BitsPerComponent": 8,
			"ColorSpace": Name("DeviceGray"), "Filter": Name("FlateDecode")},
		Data: data,
	}, Name("DeviceGray"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	g, ok := img.(*image.Gray)
	if !ok {
		t.Fatalf("got %T, want *image.Gray", img)
	}
	for _, tc := range []struct {
		x, y int
		want uint8
	}{{0, 0, 0x00}, {1, 0, 0x80}, {2, 0, 0xFF}, {0, 1, 0xFF}, {2, 1, 0x00}} {
		if got := g.GrayAt(tc.x, tc.y).Y; got != tc.want {
			t.Errorf("(%d,%d) = %#x, want %#x", tc.x, tc.y, got, tc.want)
		}
	}
}

// TestDecodeSamplesRowPadding is the shear regression test.
//
// Nine 1-bpc pixels per row means 2 bytes per row with 7 bits of padding.
// A decoder that packs rows back to back reads row 1's first pixel out of
// row 0's padding, so every row after the first is offset — the classic
// diagonal shear. Row 0 is all black and row 1 all white here, so a shear
// shows up as a wrong pixel rather than as a subtle artefact.
func TestDecodeSamplesRowPadding(t *testing.T) {
	const w, h = 9, 2
	// Row 0: nine 0 bits (black under the default DeviceGray decode).
	// Row 1: nine 1 bits (white). Both padded to 2 bytes.
	data := []byte{0x00, 0x00, 0xFF, 0x80}

	img, err := decodeImage(&Stream{
		Dict: Dict{"Width": w, "Height": h, "BitsPerComponent": 1,
			"ColorSpace": Name("DeviceGray"), "Filter": Name("FlateDecode")},
		Data: data,
	}, Name("DeviceGray"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	g := img.(*image.Gray)
	for x := 0; x < w; x++ {
		if got := g.GrayAt(x, 0).Y; got != 0x00 {
			t.Fatalf("row 0 pixel %d = %#x, want black — rows are not byte-aligned", x, got)
		}
		if got := g.GrayAt(x, 1).Y; got != 0xFF {
			t.Fatalf("row 1 pixel %d = %#x, want white — rows are not byte-aligned", x, got)
		}
	}
}

// TestDecodeSamplesRespectsDecodeArray pins /Decode inversion. Missing it
// hands OCR a negative, which is the same class of failure as the CCITT
// polarity bug — and the same cost.
func TestDecodeSamplesRespectsDecodeArray(t *testing.T) {
	dict := Dict{"Width": 2, "Height": 1, "BitsPerComponent": 8,
		"ColorSpace": Name("DeviceGray"), "Filter": Name("FlateDecode")}
	data := []byte{0x00, 0xFF}

	plain, err := decodeImage(&Stream{Dict: dict, Data: data}, Name("DeviceGray"))
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	if got := plain.(*image.Gray).GrayAt(0, 0).Y; got != 0x00 {
		t.Errorf("without /Decode: %#x, want 0x00", got)
	}

	inv := Dict{}
	for k, v := range dict {
		inv[k] = v
	}
	inv["Decode"] = Array{1, 0}
	inverted, err := decodeImage(&Stream{Dict: inv, Data: data}, Name("DeviceGray"))
	if err != nil {
		t.Fatalf("inverted: %v", err)
	}
	if got := inverted.(*image.Gray).GrayAt(0, 0).Y; got != 0xFF {
		t.Errorf("with /Decode [1 0]: %#x, want 0xFF (inversion not applied)", got)
	}
}

// TestDecodeSamplesIndexed covers the palette path, including a 4-bpc index
// (the common case for a small palette) and out-of-range clamping.
func TestDecodeSamplesIndexed(t *testing.T) {
	// Palette: 0 = red, 1 = green, 2 = blue.
	lookup := string([]byte{0xFF, 0x00, 0x00, 0x00, 0xFF, 0x00, 0x00, 0x00, 0xFF})
	cs := Array{Name("Indexed"), Name("DeviceRGB"), 2, lookup}

	// Three 4-bpc indices: 0, 1, 2 -> 0x01, 0x20 (padded).
	img, err := decodeImage(&Stream{
		Dict: Dict{"Width": 3, "Height": 1, "BitsPerComponent": 4,
			"Filter": Name("FlateDecode")},
		Data: []byte{0x01, 0x20},
	}, cs)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	rgba := img.(*image.RGBA)
	for _, tc := range []struct {
		x    int
		want color.RGBA
	}{
		{0, color.RGBA{R: 0xFF, A: 0xFF}},
		{1, color.RGBA{G: 0xFF, A: 0xFF}},
		{2, color.RGBA{B: 0xFF, A: 0xFF}},
	} {
		if got := rgba.RGBAAt(tc.x, 0); got != tc.want {
			t.Errorf("pixel %d = %v, want %v", tc.x, got, tc.want)
		}
	}
}

// TestDecodeSamplesStencil covers /ImageMask, which has no colour space and
// paints where the sample is 0.
func TestDecodeSamplesStencil(t *testing.T) {
	// 4 pixels: 1, 0, 1, 0 -> unpainted, painted, unpainted, painted.
	img, err := decodeImage(&Stream{
		Dict: Dict{"Width": 4, "Height": 1, "ImageMask": true, "Filter": Name("FlateDecode")},
		Data: []byte{0xA0}, // 1010 0000
	}, nil)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	g := img.(*image.Gray)
	want := []uint8{0xFF, 0x00, 0xFF, 0x00}
	for x, w := range want {
		if got := g.GrayAt(x, 0).Y; got != w {
			t.Errorf("stencil pixel %d = %#x, want %#x", x, got, w)
		}
	}

	// /Decode [1 0] flips which samples paint.
	inv, err := decodeImage(&Stream{
		Dict: Dict{"Width": 4, "Height": 1, "ImageMask": true,
			"Decode": Array{1, 0}, "Filter": Name("FlateDecode")},
		Data: []byte{0xA0},
	}, nil)
	if err != nil {
		t.Fatalf("inverted stencil: %v", err)
	}
	if got := inv.(*image.Gray).GrayAt(0, 0).Y; got != 0x00 {
		t.Errorf("inverted stencil pixel 0 = %#x, want 0x00", got)
	}
}

// TestDecodeSamplesICCBased confirms an ICCBased space resolves through /N
// rather than being refused. This is the single most common colour space in
// real PDFs, so refusing it would leave the raw-sample path nearly useless.
func TestDecodeSamplesICCBased(t *testing.T) {
	profile := &Stream{Dict: Dict{"N": 3}, Data: []byte("not a real profile")}
	cs := Array{Name("ICCBased"), profile}

	img, err := decodeImage(&Stream{
		Dict: Dict{"Width": 1, "Height": 1, "BitsPerComponent": 8,
			"Filter": Name("FlateDecode")},
		Data: []byte{0x10, 0x20, 0x30},
	}, cs)
	if err != nil {
		t.Fatalf("ICCBased /N 3: %v", err)
	}
	if got := img.(*image.RGBA).RGBAAt(0, 0); got != (color.RGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xFF}) {
		t.Errorf("got %v, want the raw RGB samples", got)
	}
}

// TestDecodeSamplesRejectsMalformedDicts covers the refusals, asserting on
// the *reason* rather than merely that an error came back — several of these
// would otherwise be satisfied by an incidental failure elsewhere.
func TestDecodeSamplesRejectsMalformedDicts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dict       Dict
		cs         any
		data       []byte
		wantUnsupp bool
		wantSubstr string
	}{
		{
			name:       "missing BitsPerComponent",
			dict:       Dict{"Width": 4, "Height": 1, "Filter": Name("FlateDecode")},
			cs:         Name("DeviceGray"),
			data:       []byte{0, 0, 0, 0},
			wantSubstr: "BitsPerComponent",
		},
		{
			name:       "odd bit depth",
			dict:       Dict{"Width": 4, "Height": 1, "BitsPerComponent": 3, "Filter": Name("FlateDecode")},
			cs:         Name("DeviceGray"),
			data:       []byte{0, 0, 0, 0},
			wantUnsupp: true,
		},
		{
			name:       "no colour space",
			dict:       Dict{"Width": 4, "Height": 1, "BitsPerComponent": 8, "Filter": Name("FlateDecode")},
			cs:         nil,
			data:       []byte{0, 0, 0, 0},
			wantSubstr: "ColorSpace",
		},
		{
			name:       "Separation needs a tint transform",
			dict:       Dict{"Width": 4, "Height": 1, "BitsPerComponent": 8, "Filter": Name("FlateDecode")},
			cs:         Array{Name("Separation"), Name("Spot"), Name("DeviceCMYK")},
			data:       []byte{0, 0, 0, 0},
			wantUnsupp: true,
		},
		{
			name:       "DeviceN needs a tint transform",
			dict:       Dict{"Width": 4, "Height": 1, "BitsPerComponent": 8, "Filter": Name("FlateDecode")},
			cs:         Array{Name("DeviceN"), Array{Name("A")}, Name("DeviceGray")},
			data:       []byte{0, 0, 0, 0},
			wantUnsupp: true,
		},
		{
			name:       "truncated data",
			dict:       Dict{"Width": 100, "Height": 100, "BitsPerComponent": 8, "Filter": Name("FlateDecode")},
			cs:         Name("DeviceGray"),
			data:       []byte{1, 2, 3},
			wantSubstr: "truncated",
		},
		{
			name:       "Indexed hival out of range",
			dict:       Dict{"Width": 1, "Height": 1, "BitsPerComponent": 8, "Filter": Name("FlateDecode")},
			cs:         Array{Name("Indexed"), Name("DeviceRGB"), 9999, "xxx"},
			data:       []byte{0},
			wantSubstr: "hival",
		},
		{
			name:       "Indexed lookup too short",
			dict:       Dict{"Width": 1, "Height": 1, "BitsPerComponent": 8, "Filter": Name("FlateDecode")},
			cs:         Array{Name("Indexed"), Name("DeviceRGB"), 255, "abc"},
			data:       []byte{0},
			wantSubstr: "lookup",
		},
		{
			name:       "ICCBased with unusable N",
			dict:       Dict{"Width": 1, "Height": 1, "BitsPerComponent": 8, "Filter": Name("FlateDecode")},
			cs:         Array{Name("ICCBased"), &Stream{Dict: Dict{"N": 7}}},
			data:       []byte{0},
			wantUnsupp: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeImage(&Stream{Dict: tc.dict, Data: tc.data}, tc.cs)
			if err == nil {
				t.Fatal("accepted a malformed image, want an error")
			}
			if tc.wantUnsupp && !errors.Is(err, ErrUnsupportedFilter) {
				t.Errorf("got %v, want ErrUnsupportedFilter", err)
			}
			if tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("rejected for the wrong reason: %v\nwant a message mentioning %q", err, tc.wantSubstr)
			}
		})
	}
}

// TestFlattenSMaskCompositesOverWhite pins the alpha handling.
//
// Compositing onto white matters because a transparent region handed to a
// vision model renders against whatever the model assumes — black text on a
// transparent background becomes black on black often enough to matter.
func TestFlattenSMaskCompositesOverWhite(t *testing.T) {
	// A 2x1 black image with a mask that is opaque then fully transparent.
	src := image.NewGray(image.Rect(0, 0, 2, 1))
	src.SetGray(0, 0, color.Gray{Y: 0})
	src.SetGray(1, 0, color.Gray{Y: 0})

	mask := &Stream{
		Dict: Dict{"Width": 2, "Height": 1, "BitsPerComponent": 8,
			"ColorSpace": Name("DeviceGray"), "Filter": Name("FlateDecode")},
		Data: []byte{0xFF, 0x00}, // opaque, transparent
	}

	out := flattenSMask(src, mask)
	if got := out.(*image.RGBA).RGBAAt(0, 0); got.R != 0 {
		t.Errorf("opaque pixel R = %#x, want 0x00 (source preserved)", got.R)
	}
	if got := out.(*image.RGBA).RGBAAt(1, 0); got.R != 0xFF {
		t.Errorf("transparent pixel R = %#x, want 0xFF (composited onto white)", got.R)
	}
}

// TestFlattenSMaskSurvivesUndecodableMask confirms a broken mask degrades to
// the unmasked image rather than failing the whole decode — the unmasked
// image still carries the glyphs, which is the point.
func TestFlattenSMaskSurvivesUndecodableMask(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 2, 1))
	bad := &Stream{Dict: Dict{"Width": 2, "Height": 1, "Filter": Name("JPXDecode")}, Data: []byte{1}}
	if got := flattenSMask(src, bad); got != image.Image(src) {
		t.Error("an undecodable mask should return the original image unchanged")
	}
}
