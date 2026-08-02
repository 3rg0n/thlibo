package pdf

// thlibo: local addition. Upstream has no image accessor at all — it reads
// text and tables, and `HasImages` (meta.go) only ever answered yes/no so
// filter_pdf could tell a scanned page from a blank one.
//
// This walks the content stream instead of the resource dictionary, which
// is the whole point: /Resources/XObject lists what a page *may* paint,
// not what it does, at what size, or where. A scanned page is one image
// whose CTM covers the MediaBox; a page with a logo in the corner has an
// image XObject too. Only the content stream distinguishes them, so only
// the content stream can drive the decision to hand a page to OCR.
//
// Deliberately a separate walker from extractTextWithResources rather than
// another branch inside it. That loop is validated byte-identical against
// the corpus and it is the hot path for every PDF we compress; an image
// concern threaded through it would put allocation and stream resolution
// into text extraction for no gain. The shared parts (matMul6, the inline
// image scanner) are factored, not duplicated.

import (
	"fmt"
	"image"
	"math"
)

// ImageRef is an image painted on a page, together with where it lands.
//
// Placement is what makes this useful and what a resource-dictionary walk
// cannot give you: Bounds reports the box the image occupies in default
// user space, so CoversPage can tell a full-page scan from decoration.
type ImageRef struct {
	// Name is the XObject resource name (empty for an inline image).
	Name Name

	// Inline reports an image written into the content stream with BI/ID/EI
	// rather than referenced as an XObject. Its Stream is synthetic — the
	// dict is built from the inline abbreviations, expanded to the full key
	// names so downstream code needs no special case.
	Inline bool

	// Stream carries the image dict and its data with byte-level filters
	// already applied, exactly as a stream XObject would arrive.
	Stream *Stream

	// ColorSpace is the fully resolved /ColorSpace object (Name or Array),
	// resolved here because indirect references and named resource entries
	// need a Reader and page resources that the decoder does not have.
	ColorSpace any

	// SMask is the resolved soft-mask image stream, or nil. Decode flattens
	// it onto white.
	SMask *Stream

	// CTM maps the image's unit square onto the page. The image occupies
	// [0,1]x[0,1] in image space by definition, so this matrix is the
	// placement in full — including rotation and mirroring.
	CTM [6]float64
}

// maxImagesCollected bounds the walk. A content stream can paint the same
// XObject in a loop; we only ever need enough to judge a page.
const maxImagesCollected = 4096

// PixelSize returns the image's declared sample dimensions, which are
// independent of its placement — a 4000px-wide scan can be painted into a
// 200pt box.
func (im ImageRef) PixelSize() (w, h int) {
	w, _ = im.Stream.Dict.Int("Width")
	h, _ = im.Stream.Dict.Int("Height")
	return w, h
}

// Bounds returns the axis-aligned box the image occupies in default user
// space (points), as lower-left x, y plus width and height.
//
// All four corners of the unit square are transformed rather than just
// (0,0) and (1,1): a rotated or mirrored CTM makes the naive two-corner
// version report a negative or transposed extent.
func (im ImageRef) Bounds() (x, y, w, h float64) {
	m := im.CTM
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, c := range [4][2]float64{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		px := m[0]*c[0] + m[2]*c[1] + m[4]
		py := m[1]*c[0] + m[3]*c[1] + m[5]
		minX, maxX = math.Min(minX, px), math.Max(maxX, px)
		minY, maxY = math.Min(minY, py), math.Max(maxY, py)
	}
	return minX, minY, maxX - minX, maxY - minY
}

// CoversPage reports whether the image is placed over essentially the whole
// page — the signature of a scan.
//
// The tolerance is 90% of each axis, not an exact match, because scanners
// and PDF producers routinely inset the image by a few points or place it
// slightly oversized and let the crop box clip it. Requiring exact coverage
// would reject most real scans.
func (im ImageRef) CoversPage(mediaBox [4]float64) bool {
	pw := mediaBox[2] - mediaBox[0]
	ph := mediaBox[3] - mediaBox[1]
	if pw <= 0 || ph <= 0 {
		return false
	}
	_, _, w, h := im.Bounds()
	return w >= 0.9*pw && h >= 0.9*ph
}

// ExtractPageImages returns every image painted by a page's content stream,
// in painting order, with placement resolved.
//
// Form XObjects are followed (producers routinely wrap a scan in one), with
// the form's /Matrix composed into the CTM so nested placement stays
// correct. Recursion is depth-bounded and visit-tracked: /Resources can be
// self-referential, and a hang is the one failure mode fail-open cannot
// absorb.
func ExtractPageImages(page Dict, reader *Reader) []ImageRef {
	if reader == nil {
		return nil
	}
	content, err := reader.PageContent(page)
	if err != nil || content == nil {
		return nil
	}
	return extractImages(content, reader, reader.PageResources(page),
		[6]float64{1, 0, 0, 1, 0, 0}, 0, make(map[string]bool))
}

func extractImages(content []byte, reader *Reader, resources Dict, base [6]float64, depth int, seen map[string]bool) []ImageRef {
	if depth > maxXObjectDepth {
		return nil
	}

	lex := NewLexer(content)
	var (
		out     []ImageRef
		stack   []any
		ctm     = base
		gsStack [][6]float64
	)

	for len(out) < maxImagesCollected {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			break
		}

		switch tok.Type {
		case TNumber:
			if tok.IsInt {
				stack = append(stack, tok.Int)
			} else {
				stack = append(stack, tok.Num)
			}
			continue
		case TName:
			stack = append(stack, Name(tok.Str))
			continue
		case TString, THexString:
			stack = append(stack, tok.Str)
			continue
		case TArrayStart:
			stack = append(stack, parseInlineArray(lex))
			continue
		case TDictStart:
			stack = append(stack, parseInlineDict(lex, 0))
			continue
		case TKeyword:
			// fall through to the operator switch
		default:
			continue
		}

		switch tok.Str {
		case "q":
			gsStack = append(gsStack, ctm)
		case "Q":
			if n := len(gsStack); n > 0 {
				ctm = gsStack[n-1]
				gsStack = gsStack[:n-1]
			}
		case "cm":
			if len(stack) >= 6 {
				n := len(stack)
				m := [6]float64{
					asFloat(stack[n-6]), asFloat(stack[n-5]),
					asFloat(stack[n-4]), asFloat(stack[n-3]),
					asFloat(stack[n-2]), asFloat(stack[n-1]),
				}
				ctm = matMul6(m, ctm)
			}

		case "BI":
			if ref, ok := readInlineImage(lex, reader, resources, ctm); ok {
				out = append(out, ref)
			}

		case "Do":
			if len(stack) == 0 || resources == nil {
				break
			}
			name, ok := stack[len(stack)-1].(Name)
			if !ok {
				break
			}
			xobjs, ok := reader.ResolveDict(resources["XObject"])
			if !ok {
				break
			}
			entry := xobjs[name]
			st, ok := reader.Resolve(entry).(*Stream)
			if !ok {
				break
			}
			switch sub, _ := st.Dict.Name("Subtype"); sub {
			case "Image":
				out = append(out, ImageRef{
					Name:       name,
					Stream:     st,
					ColorSpace: reader.resolveColorSpace(st.Dict["ColorSpace"], resources),
					SMask:      reader.resolveSMask(st.Dict),
					CTM:        ctm,
				})
			case "Form":
				// Cycle guard keyed on the object reference: a form whose
				// resources name itself would otherwise recurse until the
				// stack gives out.
				key, isRef := refKey(entry)
				if isRef {
					if seen[key] {
						break
					}
					seen[key] = true
				}
				formCTM := ctm
				if mArr, ok := st.Dict.Array("Matrix"); ok && len(mArr) == 6 {
					fm := [6]float64{
						asFloat(mArr[0]), asFloat(mArr[1]),
						asFloat(mArr[2]), asFloat(mArr[3]),
						asFloat(mArr[4]), asFloat(mArr[5]),
					}
					formCTM = matMul6(fm, ctm)
				}
				formRes, _ := reader.ResolveDict(st.Dict["Resources"])
				if formRes == nil {
					formRes = resources
				}
				out = append(out, extractImages(st.Data, reader, formRes, formCTM, depth+1, seen)...)
				if isRef {
					// Released after the subtree, not held for the whole
					// walk: the same form legitimately appears twice on a
					// page (a repeated header), and that is not a cycle.
					delete(seen, key)
				}
			}
		}

		stack = stack[:0]
	}

	return out
}

// resolveSMask resolves an image's /SMask to a stream, or nil.
//
// A /Mask entry is deliberately not followed: it is either a stencil mask
// image or a colour-key array, and for the OCR feed this exists to serve,
// dropping the mask entirely is better than guessing at it — an unmasked
// image still contains the glyphs.
func (r *Reader) resolveSMask(d Dict) *Stream {
	st, ok := r.Resolve(d["SMask"]).(*Stream)
	if !ok {
		return nil
	}
	return st
}

// resolveColorSpace resolves a /ColorSpace entry to a direct object.
//
// Three forms have to be handled and only the first is obvious: a direct
// name or array; an indirect reference to either; and a *resource name*
// like /CS0, which is not a colour space at all but a key into the page's
// /Resources/ColorSpace dict. Missing that third form is how an image with
// a perfectly ordinary ICCBased RGB space reads as unsupported.
//
// Nested arrays are resolved one level deep — enough for ICCBased and
// Indexed, whose components are the referenced streams and base spaces the
// decoder needs. Deeper nesting (Indexed over ICCBased over ...) resolves
// on demand in the decoder.
func (r *Reader) resolveColorSpace(obj any, resources Dict) any {
	cs := r.Resolve(obj)

	if n, ok := cs.(Name); ok && !isDeviceColorSpace(n) && resources != nil {
		// A name that is not a device space is a resource key.
		if csDict, ok := r.ResolveDict(resources["ColorSpace"]); ok {
			if entry, present := csDict[n]; present {
				cs = r.Resolve(entry)
			}
		}
	}

	if arr, ok := cs.(Array); ok {
		resolved := make(Array, len(arr))
		for i, item := range arr {
			resolved[i] = r.Resolve(item)
		}
		return resolved
	}
	return cs
}

func isDeviceColorSpace(n Name) bool {
	switch n {
	case "DeviceGray", "DeviceRGB", "DeviceCMYK", "G", "RGB", "CMYK", "Pattern":
		return true
	}
	return false
}

// inlineKeyNames expands the inline-image abbreviations to the full dict
// keys, so an inline image's dict is interchangeable with an XObject's and
// nothing downstream needs to know which it came from.
var inlineKeyNames = map[Name]Name{
	"W": "Width", "H": "Height", "BPC": "BitsPerComponent",
	"CS": "ColorSpace", "F": "Filter", "D": "Decode",
	"DP": "DecodeParms", "IM": "ImageMask", "I": "Interpolate",
	"L": "Length",
}

// inlineFilterNames expands the abbreviated filter names.
var inlineFilterNames = map[Name]Name{
	"AHx": "ASCIIHexDecode", "A85": "ASCII85Decode", "LZW": "LZWDecode",
	"Fl": "FlateDecode", "RL": "RunLengthDecode", "CCF": "CCITTFaxDecode",
	"DCT": "DCTDecode",
}

// inlineCSNames expands the abbreviated colour space names.
var inlineCSNames = map[Name]Name{
	"G": "DeviceGray", "RGB": "DeviceRGB", "CMYK": "DeviceCMYK", "I": "Indexed",
}

// readInlineImage consumes a BI...ID...EI inline image, leaving the lexer
// positioned after EI, and returns it as an ImageRef.
//
// The lexer must land past EI whether or not the caller wants the image —
// skipInlineImage is this function with the result discarded — because
// inline image data is arbitrary binary that the tokenizer would otherwise
// read as operators.
func readInlineImage(lex *Lexer, reader *Reader, resources Dict, ctm [6]float64) (ImageRef, bool) {
	d := Dict{}
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			return ImageRef{}, false
		}
		if tok.Type == TKeyword && tok.Str == "ID" {
			break
		}
		if tok.Type != TName {
			continue
		}
		key := Name(tok.Str)
		if full, ok := inlineKeyNames[key]; ok {
			key = full
		}

		val, err := lex.NextToken()
		if err != nil || val.Type == TEOF {
			return ImageRef{}, false
		}
		switch val.Type {
		case TNumber:
			if val.IsInt {
				d[key] = val.Int
			} else {
				d[key] = val.Num
			}
		case TString, THexString:
			d[key] = val.Str
		case TName:
			d[key] = expandInlineName(key, Name(val.Str))
		case TArrayStart:
			arr := parseInlineArray(lex)
			for i, item := range arr {
				if n, ok := item.(Name); ok {
					arr[i] = expandInlineName(key, n)
				}
			}
			d[key] = arr
		case TDictStart:
			d[key] = parseInlineDict(lex, 0)
		case TKeyword:
			switch val.Str {
			case "true":
				d[key] = true
			case "false":
				d[key] = false
			}
		}
	}

	// Exactly one whitespace byte separates ID from the data.
	if !lex.AtEnd() {
		lex.read()
	}
	data := scanInlineImageData(lex, d)

	// The nil-reader check comes *after* the data scan, not before: the text
	// walker calls in through skipInlineImage with a nil reader purely to
	// advance past EI, and bailing early would leave the lexer inside the
	// pixel data. Every return above this point has already consumed to EOF.
	if reader == nil {
		return ImageRef{}, false
	}
	// Apply the filter chain the same way readStreamData does, so an inline
	// image arrives in the same state as a stream XObject: byte-level
	// filters applied, image codecs left for the decoder.
	for _, f := range filterNames(d) {
		parms, _ := d.Dict("DecodeParms")
		decoded, err := applyFilter(data, f, parms)
		if err != nil {
			return ImageRef{}, false
		}
		data = decoded
	}

	return ImageRef{
		Inline:     true,
		Stream:     &Stream{Dict: d, Data: data},
		ColorSpace: reader.resolveColorSpace(d["ColorSpace"], resources),
		CTM:        ctm,
	}, true
}

func expandInlineName(key, val Name) Name {
	switch key {
	case "Filter":
		if full, ok := inlineFilterNames[val]; ok {
			return full
		}
	case "ColorSpace":
		if full, ok := inlineCSNames[val]; ok {
			return full
		}
	}
	return val
}

// scanInlineImageData returns the inline image's raw bytes and leaves the
// lexer after EI.
//
// A declared /L (/Length) is used when it is consistent with the buffer,
// because it is exact where the EI scan is a heuristic: inline image data
// is binary and can legitimately contain the byte sequence " EI ". When no
// length is declared — the common case, since /L is optional — fall back to
// scanning, which is what upstream's skip did.
func scanInlineImageData(lex *Lexer, d Dict) []byte {
	start := lex.pos
	if n, ok := d.Int("Length"); ok && n >= 0 && start+n <= len(lex.data) {
		lex.pos = start + n
		// Step over the trailing whitespace and EI.
		for !lex.AtEnd() && isWhitespace(lex.data[lex.pos]) {
			lex.pos++
		}
		if lex.pos+2 <= len(lex.data) && lex.data[lex.pos] == 'E' && lex.data[lex.pos+1] == 'I' {
			lex.pos += 2
			return lex.data[start : start+n]
		}
		// The declared length did not land on EI, so it is wrong. Rewind
		// and scan rather than trust it.
		lex.pos = start
	}

	for lex.pos < len(lex.data)-2 {
		if isWhitespace(lex.data[lex.pos]) &&
			lex.data[lex.pos+1] == 'E' && lex.data[lex.pos+2] == 'I' {
			if lex.pos+3 >= len(lex.data) || isWhitespace(lex.data[lex.pos+3]) || isDelimiter(lex.data[lex.pos+3]) {
				end := lex.pos
				lex.pos += 3
				return lex.data[start:end]
			}
		}
		lex.pos++
	}
	lex.pos = len(lex.data)
	return lex.data[start:]
}

// Decode decodes the image to a Go image, flattening any soft mask.
//
// Errors are the contract here rather than a best-effort bitmap: the caller
// is deciding whether to hand a page to OCR, and a half-decoded image is
// worse than no image at all because it looks like success.
func (im ImageRef) Decode() (image.Image, error) {
	if im.Stream == nil {
		return nil, fmt.Errorf("pdf: image has no stream")
	}
	img, err := decodeImage(im.Stream, im.ColorSpace)
	if err != nil {
		return nil, err
	}
	if im.SMask != nil {
		img = flattenSMask(img, im.SMask)
	}
	return img, nil
}
