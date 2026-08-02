package pdf

// thlibo: local addition. Raw-sample image decoding — the path
// DecodeImageStream previously refused outright.
//
// This is what "FlateDecode image" actually means: after the byte-level
// filter runs there is no codec left, just a packed array of colour
// samples, and turning it into pixels needs three things the stream itself
// does not carry — how many components a pixel has (the colour space), how
// many bits each component uses, and how to map the stored range onto the
// real one (/Decode). Get any of the three wrong and the image decodes to
// plausible-looking garbage rather than to an error, which is why every one
// of them is read explicitly here and an unrecognised value is refused.
//
// Scope is deliberately the OCR feed, not general-purpose rendering:
//
//   - Supported: DeviceGray, DeviceRGB, DeviceCMYK, Indexed, CalGray,
//     CalRGB, Lab (as its alternate), ICCBased (via /N), and stencil masks,
//     at 1, 2, 4, 8 and 16 bits per component.
//   - Refused: Separation and DeviceN, which need the tint-transform
//     function evaluated (a PostScript calculator interpreter — UniPDF
//     carries a whole `ps` package for it); and Pattern, which is not a
//     sampled image at all.
//
// Refusing those is not a gap in the OCR story. A Separation image is a
// spot-colour plate, which is not what a scanner emits, and the caller
// falls back to rasterization for it.

import (
	"fmt"
	"image"
	"image/color"
)

// sampleFormat is everything needed to walk a packed sample array,
// resolved from the image dict and its colour space.
type sampleFormat struct {
	comps  int // colour components per pixel
	bpc    int // bits per component
	space  Name
	decode []float64 // /Decode array, or nil for the default
	// palette is non-nil for Indexed: one entry per index, already
	// converted to RGBA.
	palette []color.RGBA
	stencil bool // /ImageMask true: 1-bit stencil, no colour space
}

// bytesPerRow returns the packed row stride. PDF pads every row to a byte
// boundary, so a 1-bpc 9-pixel-wide image uses 2 bytes per row, not 9 bits
// — getting this wrong shears the image diagonally, which is subtle enough
// to look like a corrupt source file rather than a decoder bug.
func (f sampleFormat) bytesPerRow(width int) int {
	bits := width * f.comps * f.bpc
	return (bits + 7) / 8
}

// decodeImage decodes an image stream given its resolved colour space.
//
// This is the colour-space-aware entry point; DecodeImageStream is the
// wrapper that reads the colour space straight out of the dict, which works
// only when it is a direct device name.
func decodeImage(st *Stream, colorSpace any) (image.Image, error) {
	if st == nil {
		return nil, fmt.Errorf("pdf: nil image stream")
	}
	w, _ := st.Dict.Int("Width")
	h, _ := st.Dict.Int("Height")
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("pdf: image has no usable dimensions (%dx%d)", w, h)
	}
	if int64(w)*int64(h) > maxImagePixels {
		return nil, fmt.Errorf("pdf: image %dx%d exceeds %d pixel cap", w, h, maxImagePixels)
	}

	names := filterNames(st.Dict)
	codec := Name("")
	if len(names) > 0 {
		codec = names[len(names)-1]
	}

	switch codec {
	case "DCTDecode":
		return decodeDCT(st)

	case "CCITTFaxDecode":
		return decodeCCITT(st, w, h)

	case "JBIG2Decode", "JPXDecode":
		// No production-grade pure-Go decoder exists for either. JPX in
		// particular is stubbed out even in the commercial Go PDF libraries,
		// so this is not a shortcut we are taking alone.
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFilter, codec)

	case "FlateDecode", "LZWDecode", "RunLengthDecode", "":
		// Raw samples: readStreamData has already run the byte-level
		// filter, so st.Data is the packed sample array.
		return decodeSamples(st, colorSpace, w, h)

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFilter, codec)
	}
}

// decodeSamples turns a packed sample array into a Go image.
func decodeSamples(st *Stream, colorSpace any, w, h int) (image.Image, error) {
	f, err := resolveSampleFormat(st.Dict, colorSpace)
	if err != nil {
		return nil, err
	}

	stride := f.bytesPerRow(w)
	need := stride * h
	if need <= 0 {
		return nil, fmt.Errorf("pdf: degenerate sample layout (%d bytes/row, %d rows)", stride, h)
	}
	if len(st.Data) < need {
		// Short buffer means the stream was truncated or the declared
		// geometry is wrong. Either way the remaining rows are unknown, and
		// padding them would hand OCR a page whose bottom half is blank
		// while reporting success.
		return nil, fmt.Errorf("pdf: image data truncated: %d bytes for %dx%d (%d needed)",
			len(st.Data), w, h, need)
	}

	switch {
	case f.stencil:
		return stencilToGray(st.Data, stride, w, h, f), nil
	case f.palette != nil:
		return indexedToRGBA(st.Data, stride, w, h, f), nil
	case f.comps == 1:
		return grayToGray(st.Data, stride, w, h, f), nil
	case f.comps == 3:
		return rgbToRGBA(st.Data, stride, w, h, f), nil
	case f.comps == 4:
		return cmykToCMYK(st.Data, stride, w, h, f), nil
	default:
		return nil, fmt.Errorf("%w: %d-component %s", ErrUnsupportedFilter, f.comps, f.space)
	}
}

// resolveSampleFormat reads the component count, bit depth and decode array.
func resolveSampleFormat(d Dict, colorSpace any) (sampleFormat, error) {
	var f sampleFormat

	// A stencil mask has no colour space and is always 1 bpc; the spec
	// permits /BitsPerComponent to be absent, so it is not read here.
	if mask, ok := d["ImageMask"].(bool); ok && mask {
		f.stencil = true
		f.comps = 1
		f.bpc = 1
		f.decode = decodeArray(d)
		return f, nil
	}

	bpc, ok := d.Int("BitsPerComponent")
	if !ok {
		return f, fmt.Errorf("pdf: image has no /BitsPerComponent")
	}
	switch bpc {
	case 1, 2, 4, 8, 16:
		f.bpc = bpc
	default:
		return f, fmt.Errorf("%w: %d bits per component", ErrUnsupportedFilter, bpc)
	}

	space, comps, palette, err := interpretColorSpace(colorSpace)
	if err != nil {
		return f, err
	}
	f.space, f.comps, f.palette = space, comps, palette
	f.decode = decodeArray(d)
	return f, nil
}

func decodeArray(d Dict) []float64 {
	arr, ok := d.Array("Decode")
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]float64, len(arr))
	for i, v := range arr {
		out[i] = asFloat(v)
	}
	return out
}

// interpretColorSpace maps a resolved colour space object to a component
// count and, for Indexed, a palette.
//
// The palette is built here rather than at sample time because an Indexed
// space's lookup table is a fixed-size blob: converting it once costs
// hival+1 conversions instead of one per pixel, and it is the only place
// the base space's own component count matters.
func interpretColorSpace(cs any) (space Name, comps int, palette []color.RGBA, err error) {
	switch v := cs.(type) {
	case Name:
		switch v {
		case "DeviceGray", "G", "CalGray":
			return v, 1, nil, nil
		case "DeviceRGB", "RGB", "CalRGB":
			return v, 3, nil, nil
		case "DeviceCMYK", "CMYK":
			return v, 4, nil, nil
		}
		return v, 0, nil, fmt.Errorf("%w: colour space %s", ErrUnsupportedFilter, v)

	case Array:
		if len(v) == 0 {
			return "", 0, nil, fmt.Errorf("pdf: empty colour space array")
		}
		family, _ := v[0].(Name)
		switch family {
		case "ICCBased":
			// The profile is a stream whose /N gives the component count.
			// Ignoring the profile itself and treating the data as the
			// matching device space is the standard fallback the spec
			// itself sanctions via /Alternate, and it is what OCR needs:
			// a luminance-accurate bitmap, not a colour-managed one.
			if len(v) < 2 {
				return family, 0, nil, fmt.Errorf("pdf: ICCBased with no profile stream")
			}
			st, ok := v[1].(*Stream)
			if !ok {
				return family, 0, nil, fmt.Errorf("pdf: ICCBased profile is not a stream")
			}
			n, ok := st.Dict.Int("N")
			if !ok {
				return family, 0, nil, fmt.Errorf("pdf: ICCBased profile has no /N")
			}
			switch n {
			case 1, 3, 4:
				return family, n, nil, nil
			}
			return family, 0, nil, fmt.Errorf("%w: ICCBased /N %d", ErrUnsupportedFilter, n)

		case "CalGray":
			return family, 1, nil, nil
		case "CalRGB", "Lab":
			// Lab is treated as three components and read as if it were
			// RGB. That is wrong colorimetrically and right for OCR: the
			// L channel dominates, so text stays dark on light.
			return family, 3, nil, nil

		case "DeviceGray":
			return family, 1, nil, nil
		case "DeviceRGB":
			return family, 3, nil, nil
		case "DeviceCMYK":
			return family, 4, nil, nil

		case "Indexed", "I":
			pal, err := buildPalette(v)
			if err != nil {
				return family, 0, nil, err
			}
			return family, 1, pal, nil

		case "Separation", "DeviceN":
			// Needs the tint-transform function evaluated. See the file
			// header: out of scope, and not what scanners emit.
			return family, 0, nil, fmt.Errorf("%w: %s needs a tint transform", ErrUnsupportedFilter, family)
		}
		return family, 0, nil, fmt.Errorf("%w: colour space family %s", ErrUnsupportedFilter, family)

	case nil:
		return "", 0, nil, fmt.Errorf("pdf: image has no /ColorSpace")
	}
	return "", 0, nil, fmt.Errorf("%w: colour space %T", ErrUnsupportedFilter, cs)
}

// maxPaletteEntries caps an Indexed space's /hival. The spec's own limit is
// 255; the cap is here because hival is file-declared and sizes a slice.
const maxPaletteEntries = 256

// buildPalette converts an [/Indexed base hival lookup] array into an RGBA
// table.
func buildPalette(arr Array) ([]color.RGBA, error) {
	if len(arr) < 4 {
		return nil, fmt.Errorf("pdf: Indexed colour space needs 4 elements, got %d", len(arr))
	}
	_, baseComps, basePalette, err := interpretColorSpace(arr[1])
	if err != nil {
		return nil, fmt.Errorf("pdf: Indexed base: %w", err)
	}
	if basePalette != nil {
		// Indexed over Indexed is legal in no version of the spec.
		return nil, fmt.Errorf("pdf: Indexed base is itself Indexed")
	}
	hival, ok := arr[2].(int)
	if !ok || hival < 0 || hival >= maxPaletteEntries {
		return nil, fmt.Errorf("pdf: Indexed /hival out of range: %v", arr[2])
	}

	var lookup []byte
	switch lv := arr[3].(type) {
	case string:
		lookup = []byte(lv)
	case *Stream:
		lookup = lv.Data
	default:
		return nil, fmt.Errorf("pdf: Indexed lookup is %T, want string or stream", arr[3])
	}

	need := (hival + 1) * baseComps
	if len(lookup) < need {
		return nil, fmt.Errorf("pdf: Indexed lookup has %d bytes, needs %d", len(lookup), need)
	}

	pal := make([]color.RGBA, hival+1)
	for i := 0; i <= hival; i++ {
		off := i * baseComps
		switch baseComps {
		case 1:
			g := lookup[off]
			pal[i] = color.RGBA{R: g, G: g, B: g, A: 0xFF}
		case 3:
			pal[i] = color.RGBA{R: lookup[off], G: lookup[off+1], B: lookup[off+2], A: 0xFF}
		case 4:
			r, g, b := cmykToRGB(lookup[off], lookup[off+1], lookup[off+2], lookup[off+3])
			pal[i] = color.RGBA{R: r, G: g, B: b, A: 0xFF}
		default:
			return nil, fmt.Errorf("%w: Indexed base with %d components", ErrUnsupportedFilter, baseComps)
		}
	}
	return pal, nil
}

// sampleAt reads the i'th component of row `row`, normalised to 0..255.
//
// Sub-byte depths are scaled rather than shifted into place: a 4-bit value
// of 15 must become 255, not 240, or a white pixel comes back slightly grey
// and every downstream threshold shifts.
func sampleAt(row []byte, index, bpc int) byte {
	switch bpc {
	case 8:
		if index >= len(row) {
			return 0
		}
		return row[index]
	case 16:
		// Take the high byte; the low byte is below OCR's noise floor.
		off := index * 2
		if off >= len(row) {
			return 0
		}
		return row[off]
	case 1, 2, 4:
		bitOff := index * bpc
		byteOff := bitOff / 8
		if byteOff >= len(row) {
			return 0
		}
		shift := 8 - bpc - (bitOff % 8)
		mask := byte((1 << bpc) - 1)
		v := (row[byteOff] >> shift) & mask
		// #nosec G115 -- bpc is 1, 2 or 4 here, so v <= mask by the masking
		// above and v*255/mask <= 255. The scaling is the point: see the
		// doc comment on why 4-bit 15 must become 255 rather than 240.
		return byte(int(v) * 255 / int(mask))
	}
	return 0
}

// applyDecode applies a /Decode pair to a normalised sample.
//
// Only inversion is handled — [1 0] rather than an arbitrary range — because
// that is the case that appears in practice (and the one that matters: a
// missed inversion hands OCR a negative). A non-trivial range is left alone
// rather than approximated.
func applyDecode(v byte, dec []float64, comp int) byte {
	if len(dec) < 2*(comp+1) {
		return v
	}
	if dec[2*comp] > dec[2*comp+1] {
		return 255 - v
	}
	return v
}

func grayToGray(data []byte, stride, w, h int, f sampleFormat) image.Image {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := data[y*stride : (y+1)*stride]
		for x := 0; x < w; x++ {
			img.SetGray(x, y, color.Gray{Y: applyDecode(sampleAt(row, x, f.bpc), f.decode, 0)})
		}
	}
	return img
}

// stencilToGray renders a 1-bit stencil mask as black-on-white.
//
// A stencil paints the current fill colour where the sample is 0 (with the
// default /Decode [0 1]) and leaves the page alone elsewhere. The fill
// colour is not tracked here, so painted samples become black: for the OCR
// feed that is the useful reading, since a stencil is nearly always black
// text or line art, and an unpainted stencil is an empty image.
func stencilToGray(data []byte, stride, w, h int, f sampleFormat) image.Image {
	img := image.NewGray(image.Rect(0, 0, w, h))
	invert := len(f.decode) >= 2 && f.decode[0] > f.decode[1]
	for y := 0; y < h; y++ {
		row := data[y*stride : (y+1)*stride]
		for x := 0; x < w; x++ {
			bitOff := x
			b := byte(0)
			if bitOff/8 < len(row) {
				b = (row[bitOff/8] >> (7 - bitOff%8)) & 1
			}
			painted := b == 0
			if invert {
				painted = !painted
			}
			v := byte(0xFF)
			if painted {
				v = 0x00
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return img
}

func indexedToRGBA(data []byte, stride, w, h int, f sampleFormat) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := data[y*stride : (y+1)*stride]
		for x := 0; x < w; x++ {
			idx := rawSampleAt(row, x, f.bpc)
			if idx >= len(f.palette) {
				// Out-of-range indices are legal-ish in the wild and the
				// spec says clamp to hival.
				idx = len(f.palette) - 1
			}
			img.SetRGBA(x, y, f.palette[idx])
		}
	}
	return img
}

// rawSampleAt reads a sample without the 0..255 normalisation sampleAt
// applies — an Indexed image's samples are palette indices, so scaling them
// would look up the wrong colour.
func rawSampleAt(row []byte, index, bpc int) int {
	switch bpc {
	case 8:
		if index >= len(row) {
			return 0
		}
		return int(row[index])
	case 16:
		off := index * 2
		if off+1 >= len(row) {
			return 0
		}
		return int(row[off])<<8 | int(row[off+1])
	case 1, 2, 4:
		bitOff := index * bpc
		byteOff := bitOff / 8
		if byteOff >= len(row) {
			return 0
		}
		shift := 8 - bpc - (bitOff % 8)
		return int((row[byteOff] >> shift) & byte((1<<bpc)-1))
	}
	return 0
}

func rgbToRGBA(data []byte, stride, w, h int, f sampleFormat) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := data[y*stride : (y+1)*stride]
		for x := 0; x < w; x++ {
			base := x * 3
			img.SetRGBA(x, y, color.RGBA{
				R: applyDecode(sampleAt(row, base, f.bpc), f.decode, 0),
				G: applyDecode(sampleAt(row, base+1, f.bpc), f.decode, 1),
				B: applyDecode(sampleAt(row, base+2, f.bpc), f.decode, 2),
				A: 0xFF,
			})
		}
	}
	return img
}

func cmykToCMYK(data []byte, stride, w, h int, f sampleFormat) image.Image {
	img := image.NewCMYK(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := data[y*stride : (y+1)*stride]
		for x := 0; x < w; x++ {
			base := x * 4
			i := img.PixOffset(x, y)
			img.Pix[i+0] = applyDecode(sampleAt(row, base, f.bpc), f.decode, 0)
			img.Pix[i+1] = applyDecode(sampleAt(row, base+1, f.bpc), f.decode, 1)
			img.Pix[i+2] = applyDecode(sampleAt(row, base+2, f.bpc), f.decode, 2)
			img.Pix[i+3] = applyDecode(sampleAt(row, base+3, f.bpc), f.decode, 3)
		}
	}
	return img
}

func cmykToRGB(c, m, yy, k byte) (r, g, b byte) {
	return color.CMYKToRGB(c, m, yy, k)
}

// flattenSMask composites an image over white using its soft mask's
// luminance as alpha.
//
// Compositing onto white rather than preserving alpha is the right call for
// an OCR feed: a transparent PNG handed to a vision model renders against
// whatever the model assumes, and black text on a transparent background
// becomes black on black often enough to matter. White is what paper is.
//
// A mask whose geometry differs from the image's is sampled by nearest
// neighbour, which the spec expects — /SMask resolution is independent of
// the image's by design.
func flattenSMask(img image.Image, mask *Stream) image.Image {
	mw, _ := mask.Dict.Int("Width")
	mh, _ := mask.Dict.Int("Height")
	if mw <= 0 || mh <= 0 {
		return img
	}
	// The mask is a DeviceGray image by definition, so it decodes without
	// needing a resolved colour space.
	m, err := decodeImage(mask, Name("DeviceGray"))
	if err != nil {
		// An undecodable mask is not worth failing the image over — the
		// unmasked image still carries the glyphs.
		return img
	}

	b := img.Bounds()
	out := image.NewRGBA(b)
	mb := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		my := mb.Min.Y + (y-b.Min.Y)*mb.Dy()/b.Dy()
		for x := b.Min.X; x < b.Max.X; x++ {
			mx := mb.Min.X + (x-b.Min.X)*mb.Dx()/b.Dx()
			mr, _, _, _ := m.At(mx, my).RGBA()
			a := float64(mr) / 0xFFFF

			r, g, bl, _ := img.At(x, y).RGBA()
			// Over white: out = src*a + 255*(1-a).
			out.SetRGBA(x, y, color.RGBA{
				R: blendOverWhite(r, a),
				G: blendOverWhite(g, a),
				B: blendOverWhite(bl, a),
				A: 0xFF,
			})
		}
	}
	return out
}

func blendOverWhite(c16 uint32, alpha float64) byte {
	c := float64(c16) / 0xFFFF * 255
	v := c*alpha + 255*(1-alpha)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v + 0.5)
}
