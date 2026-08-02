package pdf

// thlibo: local addition. Document metadata and the outline (bookmark)
// tree. Upstream gopdf reads neither — it has no /Info accessor and
// no /Outlines walk at all.
//
// Both matter for the same reason: they are the document's own summary
// of itself, stated by the producer rather than inferred from glyphs. An
// outline is the cheapest structure a PDF can hand us — a 200-page
// manual's outline is a few hundred bytes that tell a reader exactly
// where to look, and reconstructing it from page geometry is impossible.
//
// SECURITY POSTURE. Both are untrusted, attacker-influenced strings that
// end up in front of a model, so:
//
//   - Text strings are decoded, not echoed. A PDF text string is either
//     UTF-16BE with a BOM or PDFDocEncoded bytes; passing the raw bytes
//     through would emit mojibake or lone surrogates.
//   - Control characters are stripped, so a title cannot inject line
//     structure into the Markdown around it.
//   - The outline walk is depth-, count- and cycle-bounded. /Next and
//     /First chains are ordinary indirect references with nothing
//     stopping them from pointing back at an ancestor.

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// maxOutlineDepth bounds outline nesting. Real documents rarely pass 5
// levels; this is far above that.
const maxOutlineDepth = 32

// maxOutlineEntries bounds total outline items, so a generated file
// can't turn a few KB of objects into unbounded output.
const maxOutlineEntries = 5000

// OutlineItem is one entry in the document's bookmark tree.
type OutlineItem struct {
	Title string
	Depth int // 0 for top-level entries
}

// Info returns the document information dictionary, or nil.
func (r *Reader) Info() Dict {
	d, ok := r.ResolveDict(r.Trailer()["Info"])
	if !ok {
		return nil
	}
	return d
}

// InfoString returns a decoded, sanitised /Info entry (/Title,
// /Author, …), or "" when absent or unusable.
func (r *Reader) InfoString(key Name) string {
	info := r.Info()
	if info == nil {
		return ""
	}
	s, ok := r.Resolve(info[key]).(string)
	if !ok {
		return ""
	}
	return sanitizeMetaText(DecodeTextString(s))
}

// Outline returns the document's bookmark tree, flattened depth-first
// with each item's nesting depth. Empty when the document has none.
func (r *Reader) Outline() []OutlineItem {
	root, ok := r.ResolveDict(r.Trailer()["Root"])
	if !ok {
		return nil
	}
	ol, ok := r.ResolveDict(root["Outlines"])
	if !ok {
		return nil
	}
	var out []OutlineItem
	seen := make(map[string]bool)
	r.walkOutline(ol["First"], 0, seen, &out)
	return out
}

// walkOutline follows one /Next chain, recursing into /First.
//
// The visited set spans the whole walk rather than one chain, so an
// item reachable by two paths is emitted once: a cycle in either
// direction terminates, and so does a diamond.
func (r *Reader) walkOutline(obj any, depth int, seen map[string]bool, out *[]OutlineItem) {
	if depth > maxOutlineDepth {
		return
	}
	for item := obj; item != nil; {
		if len(*out) >= maxOutlineEntries {
			return
		}
		if key, isRef := refKey(item); isRef {
			if seen[key] {
				return
			}
			seen[key] = true
		}
		d, ok := r.ResolveDict(item)
		if !ok {
			return
		}
		if raw, ok := r.Resolve(d["Title"]).(string); ok {
			if title := sanitizeMetaText(DecodeTextString(raw)); title != "" {
				*out = append(*out, OutlineItem{Title: title, Depth: depth})
			}
		}
		r.walkOutline(d["First"], depth+1, seen, out)
		item = d["Next"]
	}
}

// DecodeTextString converts a PDF text string to UTF-8.
//
// The lexer hands us the string's bytes packed one-per-rune, so the two
// encodings the spec allows are still distinguishable here: a UTF-16BE
// string opens with the BOM FE FF, and anything else is PDFDocEncoded,
// whose printable range agrees with Latin-1 (so a byte is its own code
// point). Getting this wrong is not cosmetic — a UTF-16 title read as
// bytes emits a NUL between every letter.
func DecodeTextString(s string) string {
	b := []byte(s)
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		b = b[2:]
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		}
		return string(utf16.Decode(u))
	}
	// Already valid UTF-8 (common in practice, though not spec-blessed):
	// leave it alone rather than mangling every multi-byte sequence into
	// Latin-1 mojibake.
	if utf8.ValidString(s) && !strings.ContainsRune(s, 0) {
		return s
	}
	var sb strings.Builder
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// sanitizeMetaText strips control characters from a metadata string and
// collapses whitespace, so a hostile /Title cannot inject Markdown line
// structure (or a low-value sentinel) into the document around it.
func sanitizeMetaText(s string) string {
	cleaned := strings.Map(func(rr rune) rune {
		if rr == '\t' || rr == '\n' || rr == '\r' {
			return ' '
		}
		if rr < 0x20 || rr == 0x7F {
			return -1
		}
		return rr
	}, s)
	return strings.Join(strings.Fields(cleaned), " ")
}

// HasImages reports whether a page references at least one image
// XObject.
//
// This is the signal that separates a scanned page from a blank one: a
// page with no extractable text and no image is empty, while one with no
// text and a full-page image is a scan that only OCR can read. Nested
// Form XObjects are followed, since producers routinely wrap a scan in
// one; the recursion is depth-bounded and visit-tracked because /Resources
// can be self-referential.
func (r *Reader) HasImages(page Dict) bool {
	return r.hasImages(r.PageResources(page), 0, make(map[string]bool))
}

// maxXObjectDepth bounds Form XObject nesting during the image scan.
const maxXObjectDepth = 8

func (r *Reader) hasImages(res Dict, depth int, seen map[string]bool) bool {
	if res == nil || depth > maxXObjectDepth {
		return false
	}
	xobjs, ok := r.ResolveDict(res["XObject"])
	if !ok {
		return false
	}
	for _, v := range xobjs {
		if key, isRef := refKey(v); isRef {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		st, ok := r.Resolve(v).(*Stream)
		if !ok {
			continue
		}
		switch sub, _ := st.Dict.Name("Subtype"); sub {
		case "Image":
			return true
		case "Form":
			inner, _ := r.ResolveDict(st.Dict["Resources"])
			if r.hasImages(inner, depth+1, seen) {
				return true
			}
		}
	}
	return false
}
