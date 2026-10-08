package pdf

import "strings"

// fontTables is everything the text extractor needs to know about a
// content stream's fonts, keyed by resource name (/F1): how to turn a
// string's bytes into text, and how far each glyph advances.
type fontTables struct {
	toUnicode     map[string]map[uint16]string
	encodingDiffs map[string]map[byte]string
	widths        map[string]map[int]float64
	missingWidths map[string]float64
	composite     map[string]bool // Type0 (CIDFont) → 2-byte codes
}

func loadFontTables(fonts map[Name]Dict, reader *Reader) *fontTables {
	ft := &fontTables{
		toUnicode:     make(map[string]map[uint16]string),
		encodingDiffs: make(map[string]map[byte]string),
		widths:        make(map[string]map[int]float64),
		missingWidths: make(map[string]float64),
		composite:     make(map[string]bool),
	}

	for name, fd := range fonts {
		sname := string(name)
		if umap := reader.ToUnicodeMap(fd); umap != nil {
			ft.toUnicode[sname] = umap
		}
		if diffs := reader.FontEncoding(fd); diffs != nil {
			ft.encodingDiffs[sname] = diffs
		}

		subtype, _ := fd.Name("Subtype")

		if subtype == "Type0" {
			// Composite (CID) font — 2-byte character codes.
			ft.composite[sname] = true
			if descArr, ok := fd.Array("DescendantFonts"); ok && len(descArr) > 0 {
				cidFont, ok := reader.ResolveDict(descArr[0])
				if ok {
					// Default width.
					dw := 1000.0
					if v, ok := cidFont.Float("DW"); ok {
						dw = v
					}
					ft.missingWidths[sname] = dw / 1000.0

					// Sparse width array /W.
					if wArr, ok := cidFont.Array("W"); ok {
						ft.widths[sname] = parseCIDWidths(wArr)
					}

					// Font descriptor MissingWidth.
					if descRef, ok := cidFont["FontDescriptor"]; ok {
						if desc, ok := reader.ResolveDict(descRef); ok {
							if mw, ok := desc.Float("MissingWidth"); ok {
								ft.missingWidths[sname] = mw / 1000.0
							}
						}
					}
				}
			}
			continue
		}

		// Simple font — extract widths from Widths array.
		if widths, ok := fd.Array("Widths"); ok {
			wm := make(map[int]float64)
			fc, _ := fd.Int("FirstChar")
			for i, w := range widths {
				wm[fc+i] = asFloat(w)
			}
			ft.widths[sname] = wm
		}
		if mw, ok := fd.Float("MissingWidth"); ok {
			ft.missingWidths[sname] = mw
		}
		// Check font descriptor for MissingWidth.
		if descRef, ok := fd["FontDescriptor"]; ok {
			if desc, ok := reader.ResolveDict(descRef); ok {
				if mw, ok := desc.Float("MissingWidth"); ok {
					ft.missingWidths[sname] = mw
				}
			}
		}

		// Standard 14 font fallback.
		if _, ok := ft.widths[sname]; !ok {
			if baseName, ok := fd.Name("BaseFont"); ok {
				if stdW := stdFontWidths(string(baseName)); stdW != nil {
					ft.widths[sname] = stdW
				}
			}
		}
	}
	return ft
}

// charWidth returns the width of a character code (CID or byte code) in
// font, in text-space units per unit of font size.
func (ft *fontTables) charWidth(font string, code int) float64 {
	if wm, ok := ft.widths[font]; ok {
		if w, ok := wm[code]; ok {
			if ft.composite[font] {
				return w // already divided by 1000 during parsing
			}
			return w / 1000.0
		}
	}
	if mw, ok := ft.missingWidths[font]; ok {
		if ft.composite[font] {
			return mw // already divided by 1000
		}
		return mw / 1000.0
	}
	return 0.6
}

// decode turns a shown string's bytes into text: ToUnicode first, then
// /Differences, then WinAnsiEncoding (which covers most modern PDFs).
func (ft *fontTables) decode(font, s string) string {
	raw := []byte(s)
	isTwoByte := ft.composite[font]

	if umap, ok := ft.toUnicode[font]; ok && umap != nil {
		var result strings.Builder
		// For composite fonts, always use 2-byte.
		// For simple fonts, detect based on map contents.
		if !isTwoByte && len(raw) >= 2 {
			code := uint16(raw[0])<<8 | uint16(raw[1])
			if _, ok := umap[code]; ok {
				isTwoByte = true
			}
		}
		if isTwoByte && len(raw)%2 == 0 {
			for i := 0; i+1 < len(raw); i += 2 {
				code := uint16(raw[i])<<8 | uint16(raw[i+1])
				if u, ok := umap[code]; ok {
					result.WriteString(u)
				} else {
					result.WriteRune(rune(code))
				}
			}
		} else {
			for _, b := range raw {
				if u, ok := umap[uint16(b)]; ok {
					result.WriteString(u)
				} else {
					result.WriteByte(b)
				}
			}
		}
		return result.String()
	}

	if diffs, ok := ft.encodingDiffs[font]; ok && diffs != nil {
		var result strings.Builder
		for _, b := range raw {
			if name, ok := diffs[b]; ok {
				result.WriteString(glyphToString(name))
			} else {
				result.WriteByte(b)
			}
		}
		return result.String()
	}

	return winansiDecode(s)
}
