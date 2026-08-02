package pdf

// thlibo: local addition. Tagged-PDF (PDF/UA, ISO 14289) structure
// extraction. Upstream gopdf has no notion of it — /StructTreeRoot
// appears only in merge.go's copy list.
//
// Why this exists at all: every other extraction strategy *infers*
// document structure from glyph coordinates. Headings are guessed from
// font size, tables from column gaps, reading order from a Y sort. A
// tagged PDF states all three outright: /H1, /Table > /TR > /TD, and a
// tree whose order IS the authored reading order, independent of where
// the marks landed on the page. When the tags are present, reading them
// beats any heuristic — geometry engines miss tables that the document
// explicitly labels as tables.
//
// Tags are optional, so most PDFs (LaTeX output in particular) have
// none. This is a best-quality tier, not a replacement: callers fall
// back to geometry extraction when IsTagged reports false.
//
// SECURITY POSTURE. The tag tree is untrusted input and it drives what
// an AI assistant is shown, so it must not be able to assert text that
// isn't on the page. Two rules follow, both enforced here:
//
//   - Text comes only from spans whose recorded MCID matches the
//     structure element's /K reference, *on the page that element names*.
//     We never fall back to "assume structure order matches content
//     order" — a hostile or broken file could otherwise relabel
//     arbitrary content.
//   - Recursion is depth-bounded and visit-tracked. A /K chain can be
//     cyclic (nothing in the format prevents an element from being its
//     own ancestor), which would otherwise hang the process inside a
//     PreToolUse hook.
//
// The page scoping is load-bearing, not pedantry. Marked-content ids are
// unique only *within* a content stream, so real documents reuse them on
// every page: measured on a 25-page tagged PDF, 28 of 44 distinct ids
// appeared on more than one page. Keying text by id alone therefore
// merges unrelated pages into every element that shares an id — which
// both corrupts the output and breaks the guarantee above, since an
// element then surfaces text that is not its own.

import (
	"fmt"
	"strings"
)

// maxStructDepth bounds structure-tree recursion. Real documents nest a
// handful of levels (Document > Sect > Table > TR > TD > Span); this is
// far above that and well below anything that risks the stack.
const maxStructDepth = 64

// maxStructElements bounds total elements visited, so a wide-but-shallow
// bomb can't burn unbounded time.
const maxStructElements = 100_000

// IsTagged reports whether the document declares logical structure:
// /MarkInfo << /Marked true >> plus a /StructTreeRoot to walk.
//
// Both are required. A file can carry /MarkInfo without a usable tree,
// or a tree with no /MarkInfo claim; neither is safe to treat as tagged.
func (r *Reader) IsTagged() bool {
	root, ok := r.ResolveDict(r.Trailer()["Root"])
	if !ok {
		return false
	}
	mi, ok := r.ResolveDict(root["MarkInfo"])
	if !ok {
		return false
	}
	if marked, _ := mi["Marked"].(bool); !marked {
		return false
	}
	_, hasTree := r.ResolveDict(root["StructTreeRoot"])
	return hasTree
}

// mcKey identifies a marked-content sequence: an id is only unique
// within one page's content stream, so the page object number is part of
// the key.
type mcKey struct {
	page int
	mcid int
}

// structWalker carries the state of one StructuredMarkdown call.
type structWalker struct {
	r     *Reader
	spans map[mcKey][]TextSpan // (page, MCID) -> its glyph spans
	first int                  // object number of page 1, the /Pg default
	out   *strings.Builder
	seen  map[string]bool // object identity -> already visited
	count int
}

// renderSpans turns a set of spans into a single line of text, using
// BuildLines' gap analysis to decide where words actually break.
func renderSpans(spans []TextSpan) string {
	if len(spans) == 0 {
		return ""
	}
	// BuildLines sorts in place, so hand it a copy: the caller's slice is
	// shared with the walker's map and must keep its content order.
	dup := make([]TextSpan, len(spans))
	copy(dup, spans)

	var parts []string
	for _, ln := range BuildLines(dup) {
		// BuildLines pads gaps with *proportional* runs of spaces so that
		// columns line up in plain-text output. The structure path gets its
		// tables from the tag tree instead, so that padding is pure noise
		// here — collapse it, keeping only the word breaks.
		if t := strings.Join(strings.Fields(ln.Text), " "); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// StructuredMarkdown renders the document's logical structure as
// Markdown, using the tag tree rather than page geometry.
//
// Returns an error when the document is not tagged, so callers can fall
// through to the geometry path without having to pre-check.
func (r *Reader) StructuredMarkdown() (string, error) {
	if !r.IsTagged() {
		return "", fmt.Errorf("pdf: document is not tagged")
	}
	root, _ := r.ResolveDict(r.Trailer()["Root"])
	st, ok := r.ResolveDict(root["StructTreeRoot"])
	if !ok {
		return "", fmt.Errorf("pdf: no structure tree root")
	}

	pages, refs, err := r.PagesWithRefs()
	if err != nil {
		return "", err
	}

	// Build (page, MCID) -> spans. See the page-scoping note in the header.
	//
	// Spans, not strings: a span is frequently a *fragment* of a word.
	// Real producers emit "C", "loud Ap", "plicat", "ion" as four spans of
	// one marked-content sequence, so joining their text with a separator
	// invents word breaks and concatenating bare welds words together.
	// Only the glyph geometry says which it is, and BuildLines already
	// implements that judgement — so keep the spans and let it decide.
	spans := make(map[mcKey][]TextSpan)
	for i, p := range pages {
		page := refs[i]
		for _, sp := range ExtractPageText(p, r) {
			if sp.MCID < 0 {
				continue
			}
			if sp.Text == "" {
				continue
			}
			k := mcKey{page: page, mcid: sp.MCID}
			spans[k] = append(spans[k], sp)
		}
	}

	// A structure element may omit /Pg, in which case the spec says the
	// page is inherited from the nearest ancestor that has one. Page 1 is
	// the root default.
	firstPage := 0
	if len(refs) > 0 {
		firstPage = refs[0]
	}

	w := &structWalker{
		r:     r,
		spans: spans,
		first: firstPage,
		out:   &strings.Builder{},
		seen:  make(map[string]bool),
	}

	switch k := r.Resolve(st["K"]).(type) {
	case Array:
		for _, item := range k {
			w.walk(item, 0, firstPage)
		}
	default:
		w.walk(st["K"], 0, firstPage)
	}

	out := strings.TrimSpace(w.out.String())
	if out == "" {
		return "", fmt.Errorf("pdf: structure tree yielded no text")
	}
	return out + "\n", nil
}

// refKey returns a stable identity for cycle detection. Indirect
// references are the only way a cycle can form, so keying on them is
// sufficient; direct dicts are inherently acyclic in a parsed tree.
func refKey(obj any) (string, bool) {
	if ref, ok := obj.(Ref); ok {
		return fmt.Sprintf("%d:%d", ref.Num, ref.Gen), true
	}
	return "", false
}

// pageOf returns the page object number a structure element's content
// lives on: its own /Pg when present, otherwise the page inherited from
// its ancestors.
func (w *structWalker) pageOf(d Dict, inherited int) int {
	if ref, ok := d["Pg"].(Ref); ok {
		return ref.Num
	}
	return inherited
}

// mcidSpans collects the glyph spans of an element's direct
// marked-content children, on the element's own page.
//
// A /K entry may name marked content two ways, and real documents use
// both: a bare integer (the id, on the element's /Pg), or a /MCR
// dictionary that carries the id in /MCID and may redirect the page via
// its own /Pg. /OBJR references a whole object rather than marked
// content and carries no text, so it is ignored here.
//
// Nested structure elements are handled by the caller's recursion.
func (w *structWalker) mcidSpans(d Dict, inherited int) []TextSpan {
	page := w.pageOf(d, inherited)

	var out []TextSpan
	add := func(v any) {
		switch k := w.r.Resolve(v).(type) {
		case int:
			out = append(out, w.spans[mcKey{page: page, mcid: k}]...)
		case Dict:
			if typ, _ := k.Name("Type"); typ != "MCR" {
				return
			}
			id, ok := k.Int("MCID")
			if !ok {
				return
			}
			mcrPage := page
			if ref, ok := k["Pg"].(Ref); ok {
				mcrPage = ref.Num
			}
			out = append(out, w.spans[mcKey{page: mcrPage, mcid: id}]...)
		}
	}
	switch k := w.r.Resolve(d["K"]).(type) {
	case Array:
		for _, item := range k {
			add(item)
		}
	default:
		add(d["K"])
	}
	return out
}

// isContentRef reports whether a /K entry names marked content (or an
// object reference) rather than a nested structure element — i.e. whether
// mcidText already consumed it and the recursion must skip it.
func (w *structWalker) isContentRef(item any) bool {
	switch k := w.r.Resolve(item).(type) {
	case int:
		return true
	case Dict:
		typ, _ := k.Name("Type")
		return typ == "MCR" || typ == "OBJR"
	}
	return false
}

// walk emits Markdown for one structure element and recurses into its
// structure-element children.
func (w *structWalker) walk(obj any, depth, inherited int) {
	if depth > maxStructDepth || w.count > maxStructElements {
		return
	}
	if key, isRef := refKey(obj); isRef {
		if w.seen[key] {
			return // cycle
		}
		w.seen[key] = true
	}
	d, ok := w.r.ResolveDict(obj)
	if !ok {
		return
	}
	w.count++

	role, _ := d["S"].(Name)
	page := w.pageOf(d, inherited)

	// Tables own their subtree: rows and cells are emitted as a grid, so
	// the generic recursion below must not also visit them.
	if role == "Table" {
		w.out.WriteString(w.table(d, depth, page))
		return
	}

	// A block role owns its whole subtree's text. That is not a shortcut:
	// tagged producers routinely split a single paragraph across /Span,
	// /Link and /Figure children, so reading only the element's *direct*
	// marked-content ids drops the text nested one level down, and walking
	// the children as blocks of their own shatters one paragraph into
	// several.
	if md, isBlock := blockPrefix(role); isBlock {
		if t := renderSpans(w.subtreeSpans(d, depth, inherited)); t != "" {
			fmt.Fprintf(w.out, "%s%s\n\n", md, t)
		}
		return
	}

	// Grouping roles (/Document, /Sect, /Div, /NonStruct, /L …) normally
	// carry no text of their own and exist to be descended. Some do carry
	// content directly, though — /NonStruct in particular is the standard
	// wrapper for content that has no semantic role, and dropping it loses
	// real text — so emit whatever this element holds directly before
	// recursing. Block descendants are separate elements with their own
	// ids, so there is no double-count.
	if t := renderSpans(w.mcidSpans(d, inherited)); t != "" {
		fmt.Fprintf(w.out, "%s\n\n", t)
	}

	// Recurse into structure-element children, skipping the marked-content
	// entries already consumed above.
	if arr, ok := w.r.Resolve(d["K"]).(Array); ok {
		for _, item := range arr {
			if w.isContentRef(item) {
				continue
			}
			w.walk(item, depth+1, page)
		}
	} else if !w.isContentRef(d["K"]) {
		w.walk(d["K"], depth+1, page)
	}
}

// subtreeSpans gathers an element's own marked-content spans plus those
// of every descendant, in tree order — the authored reading order.
//
// Spans rather than text, so the caller can render the whole block at
// once: a word split across a /P and a nested /Span only joins correctly
// if both halves reach the same gap analysis.
//
// Visits are tracked so a cyclic /K chain terminates here too, not just in
// walk: this function recurses over the same untrusted edges.
func (w *structWalker) subtreeSpans(d Dict, depth, inherited int) []TextSpan {
	if depth > maxStructDepth || w.count > maxStructElements {
		return nil
	}
	page := w.pageOf(d, inherited)
	out := w.mcidSpans(d, inherited)

	descend := func(item any) {
		if w.isContentRef(item) {
			return
		}
		if key, isRef := refKey(item); isRef {
			if w.seen[key] {
				return
			}
			w.seen[key] = true
		}
		child, ok := w.r.ResolveDict(item)
		if !ok {
			return
		}
		w.count++
		out = append(out, w.subtreeSpans(child, depth+1, page)...)
	}

	if arr, ok := w.r.Resolve(d["K"]).(Array); ok {
		for _, item := range arr {
			descend(item)
		}
	} else {
		descend(d["K"])
	}
	return out
}

// blockPrefix maps a structure role to its Markdown prefix. The second
// return reports whether this role emits a block of its own text; a
// grouping role like /Document or /Sect carries no text and returns
// false so its children speak for it.
func blockPrefix(role Name) (string, bool) {
	switch role {
	case "H1", "H":
		return "# ", true
	case "H2":
		return "## ", true
	case "H3":
		return "### ", true
	case "H4":
		return "#### ", true
	case "H5":
		return "##### ", true
	case "H6":
		return "###### ", true
	case "P", "Note", "Caption", "Quote", "BlockQuote", "TOCI", "Index":
		return "", true
	case "LI":
		return "- ", true
	case "Code":
		return "    ", true
	default:
		// /Document, /Part, /Sect, /Art, /Div, /NonStruct, /L, /Span,
		// /Link, /Figure, /TOC and friends: grouping or inline. They are
		// descended, and any text they hold directly is still emitted —
		// see walk. /Lbl and /LBody are deliberately here rather than
		// above: they are the two halves of one /LI, so treating each as
		// its own block splits every list item in two.
		return "", false
	}
}

// table renders /Table > /TR > (/TH|/TD) as a Markdown table.
//
// Rows are located recursively because the spec permits /THead, /TBody
// and /TFoot groupings between the table and its rows — a flat scan of
// /K misses every row in a document that uses them.
func (w *structWalker) table(d Dict, depth, inherited int) string {
	var rows [][]string
	w.collectRows(d, &rows, depth, w.pageOf(d, inherited))
	if len(rows) == 0 {
		return ""
	}

	// Normalise ragged rows: a Markdown table needs a consistent column
	// count, and /TD spans (ColSpan) legitimately produce short rows.
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}

	// Drop trailing columns that are empty in every row. Real documents
	// carry spacer cells — a layout artefact of the authoring tool, not
	// data — and every one of them costs a token per row downstream.
	for width > 1 {
		used := false
		for _, r := range rows {
			if len(r) >= width && strings.TrimSpace(r[width-1]) != "" {
				used = true
				break
			}
		}
		if used {
			break
		}
		width--
	}

	fit := func(r []string) []string {
		if len(r) > width {
			r = r[:width]
		}
		for len(r) < width {
			r = append(r, "")
		}
		return r
	}

	var b strings.Builder
	fmt.Fprintf(&b, "| %s |\n", strings.Join(fit(rows[0]), " | "))
	b.WriteString("|")
	for i := 0; i < width; i++ {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, r := range rows[1:] {
		fmt.Fprintf(&b, "| %s |\n", strings.Join(fit(r), " | "))
	}
	b.WriteString("\n")
	return b.String()
}

// collectRows gathers /TR elements, descending through row-group
// wrappers.
func (w *structWalker) collectRows(d Dict, rows *[][]string, depth, inherited int) {
	if depth > maxStructDepth || w.count > maxStructElements {
		return
	}
	arr, ok := w.r.Resolve(d["K"]).(Array)
	if !ok {
		return
	}
	for _, item := range arr {
		if key, isRef := refKey(item); isRef {
			if w.seen[key] {
				continue
			}
			w.seen[key] = true
		}
		child, ok := w.r.ResolveDict(item)
		if !ok {
			continue
		}
		w.count++
		page := w.pageOf(child, inherited)
		switch role, _ := child["S"].(Name); role {
		case "TR":
			*rows = append(*rows, w.row(child, page))
		case "THead", "TBody", "TFoot":
			w.collectRows(child, rows, depth+1, page)
		}
	}
}

// row renders one /TR as its cell texts.
func (w *structWalker) row(tr Dict, inherited int) []string {
	var cells []string
	arr, ok := w.r.Resolve(tr["K"]).(Array)
	if !ok {
		return cells
	}
	page := w.pageOf(tr, inherited)
	for _, item := range arr {
		cell, ok := w.r.ResolveDict(item)
		if !ok {
			continue
		}
		switch role, _ := cell["S"].(Name); role {
		case "TH", "TD":
			cells = append(cells, w.cellText(cell, 0, page))
		}
	}
	return cells
}

// cellText collects a cell's text, including text nested inside child
// elements — a /TD commonly wraps its content in a /P.
//
// Pipes are escaped: an unescaped pipe in cell text would otherwise
// forge extra columns in the rendered Markdown, letting document content
// alter the table's apparent shape. Newlines go the same way, for the
// same reason: one would end the row early.
func (w *structWalker) cellText(d Dict, depth, inherited int) string {
	t := renderSpans(w.subtreeSpans(d, depth, inherited))
	t = strings.ReplaceAll(t, "|", "\\|")
	return strings.Join(strings.Fields(t), " ")
}
