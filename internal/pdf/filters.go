package pdf

// thlibo: local addition. Two jobs.
//
// 1. filterNames is carried over verbatim from upstream's merge.go,
//    which we dropped (generation code). readStreamData needs it.
//
// 2. Image-XObject filter decoding. Upstream's applyFilter ends in
//    `default: return data, nil` — an unknown filter yields the
//    *undecoded* bytes with no error. For xref and content streams
//    that is harmless in practice (they are Flate or LZW), but for
//    image XObjects it is actively wrong: a CCITTFax or DCT stream
//    returned raw looks like a successfully decoded image of the same
//    length. That matters because extracting image XObjects is how we
//    reach scanned pages without rasterizing them.
//
// The split is deliberate: DecodeImageStream is a separate entry point
// rather than a change to applyFilter, so the parsing path upstream
// already exercises keeps its exact behaviour and only the new
// image path gets the stricter treatment.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"

	"golang.org/x/image/ccitt"
)

// filterNames extracts the filter name(s) from a stream dict's /Filter entry.
func filterNames(d Dict) []Name {
	if f, ok := d.Name("Filter"); ok {
		return []Name{f}
	}
	if fa, ok := d.Array("Filter"); ok {
		var names []Name
		for _, item := range fa {
			if n, ok := item.(Name); ok {
				names = append(names, n)
			}
		}
		return names
	}
	return nil
}

// ErrUnsupportedFilter reports an image stream we cannot decode in pure
// Go. JBIG2Decode and JPXDecode (JPEG 2000) have no pure-Go decoder;
// pulling in a CGO one would contradict the no-CGO property that makes
// this package safe to vendor, so we refuse and let the caller fall
// back (to OCR, or to the original bytes).
var ErrUnsupportedFilter = errors.New("pdf: unsupported image filter")

// maxImagePixels bounds decoded image dimensions to block decompression
// bombs — a 100-byte stream can declare a 50000x50000 image. The limit
// mirrors internal/pdfocr's posture: check declared geometry before
// allocating anything proportional to it.
const maxImagePixels = 80 << 20 // 80 Mpx: generous for a 600 DPI page

// DecodeImageStream decodes an image XObject's stream to a Go image.
//
// Returns ErrUnsupportedFilter for JBIG2/JPX, and an error (never
// partial garbage) for a stream whose declared geometry is implausible.
// A scanned page is typically one full-page image XObject, so this is
// the lossless alternative to re-rasterizing a page we can already read.
//
// thlibo: the colour space is read straight from the dict here, which is
// correct only when it is a direct device name. Anything else — an indirect
// reference, an ICCBased array, or a /CS0-style resource key — needs the
// Reader and page resources to resolve, so callers that have them should go
// through ImageRef.Decode (images.go) instead. This entry point stays for
// the codecs that carry their own colour information (DCT, CCITT).
func DecodeImageStream(st *Stream) (image.Image, error) {
	if st == nil {
		return nil, errors.New("pdf: nil image stream")
	}
	return decodeImage(st, st.Dict["ColorSpace"])
}

// decodeDCT decodes a JPEG-compressed image stream.
//
// st.Data is undecoded for DCT (applyFilter's default branch returns it
// as-is), so the JPEG bytes are exactly what image/jpeg wants.
func decodeDCT(st *Stream) (image.Image, error) {
	img, err := jpeg.Decode(bytes.NewReader(st.Data))
	if err != nil {
		return nil, fmt.Errorf("pdf: jpeg: %w", err)
	}
	return img, nil
}

// decodeCCITT decodes a CCITT Group 3/4 fax image — the common encoding
// for black-and-white scans.
func decodeCCITT(st *Stream, w, h int) (image.Image, error) {
	sub := ccitt.Group4
	order := ccitt.MSB
	blackIs1 := false

	if dp, ok := st.Dict.Dict("DecodeParms"); ok {
		if k, ok := dp.Int("K"); ok && k >= 0 {
			// K < 0 is Group 4; K == 0 is Group 3 1-D; K > 0 is mixed
			// 2-D, which x/image/ccitt models as Group 3 as well.
			sub = ccitt.Group3
		}
		if b, ok := dp["BlackIs1"].(bool); ok {
			blackIs1 = b
		}
	}

	// thlibo: DecodeIntoGray, not NewReader. NewReader yields *packed 1-bpp*
	// rows (one bit per pixel), so filling an image.Gray's 1-byte-per-pixel
	// Pix with io.ReadFull demanded exactly 8x the bytes the reader produces
	// and every CCITT image failed with "unexpected EOF" — measured on a real
	// 2480x3507 fax scan: 1,087,170 bytes delivered against 8,697,360 wanted.
	// DecodeIntoGray does the bit-to-byte expansion itself.
	//
	// Invert tracks blackIs1 rather than negating it. x/image/ccitt already
	// emits CCITT's default polarity (0 bits = white) as white pixels, so
	// inverting on the default turns a text page into a 94.8%-black one;
	// /BlackIs1 true is the case that needs the flip. Measured on the same
	// scan: 5.21% dark with this mapping, 94.79% with the negation.
	opts := &ccitt.Options{Invert: blackIs1}
	gray := image.NewGray(image.Rect(0, 0, w, h))
	if err := ccitt.DecodeIntoGray(gray, bytes.NewReader(st.Data), order, sub, opts); err != nil {
		return nil, fmt.Errorf("pdf: ccitt: %w", err)
	}
	return gray, nil
}
