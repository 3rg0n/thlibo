package pdf

import (
	"math"
	"sort"
	"strings"
)

// TextSpan is a piece of text with its position on the page.
type TextSpan struct {
	X, Y     float64
	EndX     float64 // X position after this span (for accurate gap detection)
	FontSize float64
	Font     string
	Text     string

	// MCID is the marked-content id enclosing this span, or -1 when the
	// span sits outside any BDC/EMC pair.
	//
	// thlibo: added. A tagged PDF's structure tree references text by
	// MCID, so without this the only way to tie a /TD element to its
	// text is to assume structure order matches content order — exactly
	// the assumption a malformed or hostile file breaks. We record the
	// real id and let the walker match on it.
	MCID int
}

// TextLine is a reconstructed line of text.
type TextLine struct {
	Y     float64
	Spans []TextSpan
	Text  string
}

// ExtractText extracts positioned text spans from a PDF content stream.
func ExtractText(content []byte, fonts map[Name]Dict, reader *Reader) []TextSpan {
	return ExtractTextWithResources(content, fonts, reader, nil)
}

// ExtractTextWithResources extracts text with access to full page resources
// (needed for Form XObject extraction via the Do operator).
func ExtractTextWithResources(content []byte, fonts map[Name]Dict, reader *Reader, resources Dict) []TextSpan {
	return extractTextWithResources(content, fonts, reader, resources, 0)
}

// ExtractPageText extracts text from a page, handling rotation and resources automatically.
func ExtractPageText(page Dict, reader *Reader) []TextSpan {
	content, err := reader.PageContent(page)
	if err != nil || content == nil {
		return nil
	}
	fonts := reader.PageFonts(page)
	resources := reader.PageResources(page)
	spans := extractTextWithResources(content, fonts, reader, resources, 0)

	// Apply page rotation to all span positions.
	rotate, _ := page.Int("Rotate")
	if rotate == 0 {
		return spans
	}

	// Get page origin and dimensions from MediaBox [llx lly urx ury]. The
	// origin (x0, y0) is carried through so rotated span positions stay in the
	// page's displayed space about its true origin — the inverse of the map
	// rotateOverlaySpace applies to overlays, so the two round-trip exactly.
	var x0, y0, width, height float64
	if mb, ok := page.Array("MediaBox"); ok && len(mb) >= 4 {
		x0 = asFloat(mb[0])
		y0 = asFloat(mb[1])
		width = asFloat(mb[2]) - x0
		height = asFloat(mb[3]) - y0
	}

	var rotM [6]float64
	switch rotate % 360 {
	case 90:
		rotM = [6]float64{0, -1, 1, 0, x0 - y0, width + x0 + y0}
	case 180:
		rotM = [6]float64{-1, 0, 0, -1, width + 2*x0, height + 2*y0}
	case 270:
		rotM = [6]float64{0, 1, -1, 0, height + x0 + y0, y0 - x0}
	default:
		return spans
	}

	for i := range spans {
		x := rotM[0]*spans[i].X + rotM[2]*spans[i].Y + rotM[4]
		y := rotM[1]*spans[i].X + rotM[3]*spans[i].Y + rotM[5]
		spans[i].X = x
		spans[i].Y = y
		if spans[i].EndX != 0 {
			ex := rotM[0]*spans[i].EndX + rotM[2]*spans[i].Y + rotM[4]
			spans[i].EndX = ex
		}
	}

	return spans
}

func extractTextWithResources(content []byte, fonts map[Name]Dict, reader *Reader, resources Dict, depth int) []TextSpan {
	// Bounds Form XObject recursion (doXObject), including a form that
	// invokes itself.
	const maxDepth = 10
	if depth > maxDepth {
		return nil
	}
	x := &textExtractor{
		fonts:     fonts,
		ft:        loadFontTables(fonts, reader),
		reader:    reader,
		resources: resources,
		depth:     depth,
		ctm:       identityMatrix,
		th:        100,
	}
	x.run(NewLexer(content))
	return x.spans
}

func parseInlineArray(lex *Lexer) Array {
	var arr Array
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF || tok.Type == TArrayEnd {
			break
		}
		switch tok.Type {
		case TNumber:
			if tok.IsInt {
				arr = append(arr, tok.Int)
			} else {
				arr = append(arr, tok.Num)
			}
		case TString, THexString:
			arr = append(arr, tok.Str)
		case TName:
			arr = append(arr, Name(tok.Str))
		case TArrayStart:
			arr = append(arr, parseInlineArray(lex))
		}
	}
	return arr
}

// decodeActualText handles ActualText strings which may be UTF-16BE with BOM.
func decodeActualText(s string) string {
	raw := []byte(s)
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		// UTF-16BE with BOM.
		var runes []rune
		for i := 2; i+1 < len(raw); i += 2 {
			u := rune(raw[i])<<8 | rune(raw[i+1])
			// Handle surrogate pairs.
			if u >= 0xD800 && u <= 0xDBFF && i+3 < len(raw) {
				lo := rune(raw[i+2])<<8 | rune(raw[i+3])
				if lo >= 0xDC00 && lo <= 0xDFFF {
					u = 0x10000 + (u-0xD800)*0x400 + (lo - 0xDC00)
					i += 2
				}
			}
			runes = append(runes, u)
		}
		return string(runes)
	}
	return s
}

// parseInlineDict parses a content-stream inline dict, the << already
// consumed. It replaces the previous skipInlineDict: BDC's properties
// operand is an inline dict, so the value has to survive to the operator.
//
// Nesting is depth-bounded because content streams are untrusted input
// and nothing stops a producer from emitting << << << ... forever.
// Beyond the bound the remaining dict is consumed but discarded, so the
// lexer still lands after the matching >> and the operator stream stays
// in sync.
func parseInlineDict(lex *Lexer, depth int) Dict {
	const maxInlineDictDepth = 16

	d := Dict{}
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF || tok.Type == TDictEnd {
			return d
		}
		// Keys are names; anything else at key position is malformed, so
		// skip the token and resynchronise rather than misalign values.
		if tok.Type != TName {
			continue
		}
		key := Name(tok.Str)

		val, err := lex.NextToken()
		if err != nil || val.Type == TEOF {
			return d
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
			d[key] = Name(val.Str)
		case TArrayStart:
			d[key] = parseInlineArray(lex)
		case TDictStart:
			if depth+1 >= maxInlineDictDepth {
				skipInlineDict(lex)
				continue
			}
			d[key] = parseInlineDict(lex, depth+1)
		case TKeyword:
			switch val.Str {
			case "true":
				d[key] = true
			case "false":
				d[key] = false
			case "null":
				d[key] = nil
			}
		case TDictEnd:
			// Key with no value; the dict ended here.
			return d
		}
	}
}

func skipInlineDict(lex *Lexer) {
	depth := 1
	for depth > 0 {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			return
		}
		if tok.Type == TDictStart {
			depth++
		}
		if tok.Type == TDictEnd {
			depth--
		}
	}
}

// skipInlineImage consumes a BI...ID...EI inline image without keeping it.
//
// thlibo: this is readInlineImage (images.go) with the result discarded, and
// it delegates rather than duplicating the scan. Landing the lexer past EI
// is the load-bearing part in both cases — inline image data is arbitrary
// binary, and a tokenizer let loose in it reads pixel bytes as operators.
func skipInlineImage(lex *Lexer) {
	_, _ = readInlineImage(lex, nil, nil, [6]float64{})
}

// BuildLines groups text spans into lines and reconstructs text.
func BuildLines(spans []TextSpan) []TextLine {
	if len(spans) == 0 {
		return nil
	}

	// Group spans by Y coordinate (with tolerance).
	// Keep tight to avoid merging overlapping text layers at similar Y positions.
	const yTolerance = 1.0
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].Y > spans[j].Y // top to bottom
	})

	var lines []TextLine
	var currentLine *TextLine

	for _, span := range spans {
		if currentLine == nil || math.Abs(span.Y-currentLine.Y) > yTolerance {
			lines = append(lines, TextLine{Y: span.Y})
			currentLine = &lines[len(lines)-1]
		}
		currentLine.Spans = append(currentLine.Spans, span)
	}

	// Sort spans within each line by X and build text.
	for i := range lines {
		sortSpansByX(lines[i].Spans)

		var buf strings.Builder
		prevEnd := -1.0
		for _, span := range lines[i].Spans {
			if prevEnd >= 0 {
				gap := span.X - prevEnd
				spaceWidth := span.FontSize * 0.25
				if spaceWidth < 2 {
					spaceWidth = 2
				}
				if gap > spaceWidth {
					// Insert proportional spaces.
					nSpaces := int(gap / spaceWidth)
					if nSpaces < 1 {
						nSpaces = 1
					}
					if nSpaces > 10 {
						nSpaces = 10 // cap at reasonable tab-like spacing
					}
					buf.WriteString(strings.Repeat(" ", nSpaces))
				} else if gap > 0.5 {
					buf.WriteByte(' ')
				}
			}
			buf.WriteString(span.Text)
			// Estimate where this span ends.
			if span.EndX > span.X {
				prevEnd = span.EndX
			} else {
				prevEnd = span.X + float64(len([]rune(span.Text)))*span.FontSize*0.5
			}
		}
		lines[i].Text = buf.String()
	}

	return lines
}

// glyphToString converts a PostScript glyph name to its Unicode string.
func glyphToString(name string) string {
	// Common glyph names.
	if r, ok := glyphMap[name]; ok {
		return string(r)
	}
	// If it looks like "uniXXXX", decode hex.
	if strings.HasPrefix(name, "uni") && len(name) == 7 {
		v, err := parseHexRune(name[3:])
		if err == nil {
			return string(v)
		}
	}
	if len(name) == 1 {
		return name
	}
	return name
}

func parseHexRune(s string) (rune, error) {
	var v rune
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= c - '0'
		case c >= 'a' && c <= 'f':
			v |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v |= c - 'A' + 10
		default:
			return 0, nil
		}
	}
	return v, nil
}

// glyphMap is defined in glyphlist.go (generated from Adobe Glyph List).

// winansiDecode converts a WinAnsiEncoding string to UTF-8.
func winansiDecode(s string) string {
	var buf strings.Builder
	for _, b := range []byte(s) {
		if r, ok := winansiMap[b]; ok {
			buf.WriteRune(r)
		} else {
			buf.WriteByte(b)
		}
	}
	return buf.String()
}

// WinAnsiEncoding special mappings (0x80-0x9F differ from Latin-1).
var winansiMap = map[byte]rune{
	0x80: '\u20AC', 0x82: '\u201A', 0x83: '\u0192', 0x84: '\u201E',
	0x85: '\u2026', 0x86: '\u2020', 0x87: '\u2021', 0x88: '\u02C6',
	0x89: '\u2030', 0x8A: '\u0160', 0x8B: '\u2039', 0x8C: '\u0152',
	0x8E: '\u017D', 0x91: '\u2018', 0x92: '\u2019', 0x93: '\u201C',
	0x94: '\u201D', 0x95: '\u2022', 0x96: '\u2013', 0x97: '\u2014',
	0x98: '\u02DC', 0x99: '\u2122', 0x9A: '\u0161', 0x9B: '\u203A',
	0x9C: '\u0153', 0x9E: '\u017E', 0x9F: '\u0178',
}

// parseCIDWidths parses a CIDFont /W array into a cid→width map.
// Format: [ cid_start [w1 w2 ...] ] or [ cid_start cid_end w ]
func parseCIDWidths(wArr Array) map[int]float64 {
	wm := make(map[int]float64)
	i := 0
	for i < len(wArr) {
		cid := asInt(wArr[i])
		i++
		if i >= len(wArr) {
			break
		}
		switch v := wArr[i].(type) {
		case Array:
			// cid_start [w1 w2 w3 ...]
			for j, w := range v {
				wm[cid+j] = asFloat(w) / 1000.0
			}
			i++
		default:
			// cid_start cid_end width
			if i+1 >= len(wArr) {
				break
			}
			cidEnd := asInt(wArr[i])
			i++
			width := asFloat(wArr[i]) / 1000.0
			i++
			for c := cid; c <= cidEnd; c++ {
				wm[c] = width
			}
		}
	}
	return wm
}
