package processors

// pdf-filter: convert a PDF to Markdown in-process, in pure Go.
//
// This is the native replacement for the pdf-to-md Python script (ADR
// 0007). The script needs pypdf + pdfplumber installed, costs a Python
// interpreter launch per invocation, and — measured on this repo's
// corpus — extracts text ~156x slower than the vendored Go engine with
// more word mangling (6.39% vs 0.11% of words), so the Python
// dependency was buying nothing on the text path.
//
// TIER LADDER. Four strategies, best evidence first. Each tier states
// something the next one has to guess:
//
//  1. **Tag tree** (/StructTreeRoot). The producer states headings,
//     tables and reading order outright. When present this beats every
//     heuristic — geometry finds 1 table in a 30-page guide where the
//     tags declare 8.
//  2. **Native text extraction**. Glyphs with positions, grouped into
//     lines by gap analysis. What the overwhelming majority of PDFs get.
//  3. **Geometry tables**. Column detection over the same spans, for
//     documents that lay out tables without tagging them.
//  4. **OCR**, for scanned pages — not done here. A page with images and
//     no text emits the low-value sentinel and `thlibo case` takes over
//     with inferd vision (ADR 0009). This filter is text-only, exactly
//     as the script was.
//
// Tiers 1 and 3 are complementary rather than ranked, so tier 1 output
// is *augmented* with geometry tables rather than replacing them (see
// pdfMarkdown). A tag tree only describes what the producer bothered to
// mark: measured on a 30-page tagged guide the tree declared 8 tables
// where geometry found 1, and the reverse case — a document that lays
// out real tables without tagging them — is exactly what tier 3 is for.
//
// FAIL-OPEN CONTRACT (CLAUDE.md invariant #2 / ADR 0006). A native
// filter's return value IS the tool output, so returning a diagnostic
// string would replace the document with an error message. Every failure
// path here returns the input unchanged instead, which RunNative's
// monotonic guard then passes through verbatim. Encrypted documents are
// refused for exactly this reason: they parse fine and extract
// ciphertext, so without the check the output would be plausible-looking
// garbage with no error anywhere.

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/3rg0n/thlibo/internal/pdf"
)

func init() { RegisterNative("pdf-filter", pdfFilter) }

// LowValueSentinel is the marker appended when no page yielded
// extractable text — a scanned document. `internal/casefile` detects it,
// strips it, and hands the raw PDF to the vision OCR path (ADR 0009);
// when OCR is unreachable the placeholder output survives (fail-open).
//
// Exported so casefile and this package can't drift apart on the exact
// bytes. Format is deliberately noisy so nothing produces it by
// accident.
const LowValueSentinel = "<!-- thlibo-pdf-low-value: no extractable text -->"

// maxPDFPages bounds how many pages are rendered. A 2000-page manual
// would otherwise produce output far past any context window, and the
// per-page cost is real (text extraction is linear in glyphs). The cap
// is noted in the output so a reader knows the document was truncated
// rather than short.
const maxPDFPages = 200

func pdfFilter(raw []byte) []byte {
	// Cheap gate first: the signature must be within the first 1 KiB
	// (some transports prepend an envelope; the reader tolerates that and
	// so must this check). Anything else is not ours.
	head := raw
	if len(head) > 1024 {
		head = head[:1024]
	}
	if !bytes.Contains(head, []byte("%PDF-")) {
		return raw
	}

	doc, err := pdf.OpenBytes(raw)
	if err != nil {
		return raw
	}
	// Encrypted: we decrypt nothing, so every string and stream would
	// extract as ciphertext with no error to key off. Refuse.
	if doc.IsEncrypted() {
		return raw
	}

	md := pdfMarkdown(doc)
	if strings.TrimSpace(md) == "" {
		return raw
	}
	return []byte(md)
}

// pdfMarkdown renders a whole document. Assembly order mirrors what a
// reader needs first: title, page count, outline, then the pages.
func pdfMarkdown(doc *pdf.Document) string {
	var parts []string

	if title := doc.InfoString("Title"); title != "" && !metaPlaceholder(title) {
		parts = append(parts, "# "+title)
	}
	var front []string
	if author := doc.InfoString("Author"); author != "" && !metaPlaceholder(author) {
		front = append(front, "- author: "+author)
	}
	front = append(front, fmt.Sprintf("- pages: %d", doc.NumPages()))
	parts = append(parts, strings.Join(front, "\n"))

	if ol := renderOutline(doc.Outline()); ol != "" {
		parts = append(parts, ol)
	}

	// Tier 1. The tag tree covers the whole document at once (its order
	// IS the reading order, independent of pagination), so it is rendered
	// as one block rather than per page.
	//
	// It is used *in addition to* the page pass, not instead of it: the
	// tree states headings and tables the geometry can't see, while the
	// page pass reaches text no element claims — an untagged figure
	// caption, a page a producer forgot to mark. The cost is paid in
	// renderPages, which suppresses per-page body text when the tree
	// already carried the prose (see its haveStructure parameter).
	structured := ""
	if doc.IsTagged() {
		if s, err := doc.StructuredMarkdown(); err == nil {
			structured = s
		}
	}

	pages, textPages, truncated := renderPages(doc, structured != "")
	if structured != "" {
		parts = append(parts, "## Document structure", strings.TrimSpace(structured))
	}
	parts = append(parts, pages...)
	if truncated {
		parts = append(parts, fmt.Sprintf(
			"_[document truncated: %d of %d pages rendered]_", maxPDFPages, doc.NumPages()))
	}

	// Low-value: no page produced extractable text, so the output is
	// nothing but placeholders and only OCR can help. The structure tree
	// counts as text — a tagged document that reached this point has real
	// content even if the page pass found none.
	if doc.NumPages() > 0 && textPages == 0 && strings.TrimSpace(structured) == "" {
		parts = append(parts, LowValueSentinel)
	}

	return strings.Join(parts, "\n\n") + "\n"
}

// renderPages runs the per-page pass, returning the page blocks, how
// many pages yielded real text, and whether the page cap truncated the
// document.
//
// haveStructure suppresses per-page body text: when the tag tree already
// rendered the document's prose, repeating every page verbatim would
// roughly double the output for no added signal. Tables and page
// placeholders still come through, since those are what the tree misses.
func renderPages(doc *pdf.Document, haveStructure bool) (blocks []string, textPages int, truncated bool) {
	n := doc.NumPages()
	if n > maxPDFPages {
		n = maxPDFPages
		truncated = true
	}

	// Two passes. Running headers and footers are only identifiable
	// document-wide — a line is furniture because it recurs across pages,
	// which no single page can tell you — so collect all the text first.
	type pageData struct {
		lines  []string
		spans  []pdf.TextSpan
		images bool
	}
	data := make([]pageData, 0, n)
	texts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p := doc.Page(i)
		if p == nil {
			continue
		}
		spans, _ := p.TextSpans()
		lines, _ := p.TextLines()
		var ls []string
		for _, ln := range lines {
			ls = append(ls, strings.TrimRight(ln.Text, " "))
		}
		data = append(data, pageData{lines: ls, spans: spans, images: p.HasImages()})
		texts = append(texts, strings.Join(ls, "\n"))
	}

	furniture := findFurniture(texts)

	for i, pd := range data {
		num := i + 1
		block := []string{fmt.Sprintf("## Page %d", num)}

		// Classify on the *raw* text: furniture stripping is a
		// presentation concern and must not turn a text page into a
		// "scanned" one, which would wrongly trigger the OCR path.
		switch pageStrategy(texts[i], pd.images) {
		case pageText:
			textPages++
			if !haveStructure {
				body := stripFurniture(pd.lines, furniture)
				// A page that is nothing but furniture keeps its original
				// text rather than emitting an empty section.
				if strings.TrimSpace(body) == "" {
					body = strings.TrimSpace(texts[i])
				}
				block = append(block, promoteHeadings(body))
			}
			if tbl := renderPageTables(pd.spans, num); tbl != "" {
				block = append(block, tbl)
			}
			if pd.images {
				block = append(block, fmt.Sprintf("_[images on page %d]_", num))
			}
		case pageScanned:
			// Placeholder only; the Go caller OCRs image-only pages via
			// inferd vision (ADR 0009). Survives only if that's unreachable.
			block = append(block, fmt.Sprintf("_[scanned page %d]_", num))
		case pageChart:
			block = append(block, fmt.Sprintf("_[chart on page %d]_", num))
			if t := strings.TrimSpace(texts[i]); t != "" {
				// Surface what little text there was: axis labels, a caption.
				block = append(block, "```\n"+t+"\n```")
			}
		default:
			block = append(block, "_[blank page]_")
		}

		// With structure rendered, a page whose only content was body text
		// contributes just its heading — drop it rather than emitting a
		// run of bare "## Page N" lines.
		if haveStructure && len(block) == 1 {
			continue
		}
		blocks = append(blocks, strings.Join(block, "\n\n"))
	}
	return blocks, textPages, truncated
}

// renderPageTables emits tier 3: tables recovered from glyph geometry.
//
// Layout grids are rejected the same way the Python filter rejects
// pdfplumber's: a single-column "table" carries no row/column
// relationship, and a grid whose cells hold whole paragraphs is a
// slide-deck layout frame whose text is already in the page body.
func renderPageTables(spans []pdf.TextSpan, page int) string {
	tables := pdf.FindTables(spans, nil)
	var out []string
	j := 0
	for i := range tables {
		if isLayoutGrid(&tables[i]) {
			continue
		}
		rendered := renderTable(&tables[i])
		if rendered == "" {
			continue
		}
		j++
		out = append(out, fmt.Sprintf("### Table %d.%d", page, j), rendered)
	}
	return strings.Join(out, "\n\n")
}

// Layout-grid rejection thresholds. The first two are carried over from
// the Python filter, where they were measured on a 35-slide deck: real
// tables held 212 chars in their longest cell and were >=46% filled,
// while the layout grids started at 1097 chars and sat at 11-30% filled.
//
// The rest are new, and they exist because this filter's tables come
// from *gap analysis over glyph positions* rather than pdfplumber's word
// boxes and ruling lines. That detector has a failure mode pdfplumber
// doesn't: justified prose and dotted-leader TOC lines contain wide
// inter-word gaps, so a single line of text gets read as a row of
// columns. Measured on a 384-page manual, this produced 13 "tables",
// every one of them a shredded TOC entry or paragraph.
const (
	maxTableCellChars = 1000
	minTableDensity   = 0.40
	// A real data table repeats its shape: header plus at least two rows.
	// One row is a single line of text that happened to have gaps in it,
	// and there is no way to tell the difference from the geometry alone.
	minTableDataRows = 2
	// Above this fraction of single-character tokens, the cells are
	// letter-shredded glyph runs ("c r y p t l i b s o f"), not words.
	maxTableSingleCharFrac = 0.30
	// Above this fraction of single-character *column names*, the header
	// row isn't one. A real table names its columns ("Raised By:",
	// "Model"); a paragraph sliced at its inter-word gaps yields headers
	// like "t | R | r | m | s | s | s" — the detector landed the column
	// boundaries mid-word, which is proof the columns aren't real.
	maxHeaderSingleCharFrac = 0.50
	// Above this fraction of adjacent cell pairs whose glyphs nearly
	// touch on the page, the column boundaries are fake.
	//
	// This is the one test here that is evidence rather than a tuned
	// threshold, and it is the only one that reliably separates the two
	// populations. A column boundary is *space*: adjacent cells in a real
	// table are separated by a visible gutter, because that gutter is how
	// a human reader sees the columns at all. When the last glyph of one
	// cell ends where the next cell's first glyph begins, nothing was
	// there to divide them — the detector split one continuous line of
	// text and called the halves columns.
	//
	// Measured over 41 documents: every table whose columns were real
	// scored 0.00, while the paragraph-and-TOC false positives scored
	// 0.47 to 1.00. Nothing landed in between, so the threshold sits in
	// the middle of an empty gap rather than on a judgement call.
	maxTightJunctionFrac = 0.25
	// minGutterFraction is how wide a gutter must be, as a fraction of
	// the font size, to count as a real column separator. A quarter of an
	// em is narrower than an inter-word space in every font — so this
	// only fires when the glyphs are genuinely adjacent, not merely
	// close.
	minGutterFraction = 0.25
)

// isLayoutGrid reports whether a detected table is really an invisible
// layout frame or a line of prose that gap analysis mistook for columns.
//
// Rejecting one loses nothing: the text is already in the page body, so
// dropping the table drops a duplicate — and for the prose case the
// table version is the *worse* copy, since the column split cuts words
// in half.
func isLayoutGrid(t *pdf.Table) bool {
	if t == nil || len(t.Columns) < 2 || len(t.Rows) < minTableDataRows {
		return true
	}
	total, filled, longest := 0, 0, 0
	tokens, singles := 0, 0
	for _, r := range t.Rows {
		total += len(t.Columns)
		for _, c := range r.Cells {
			s := strings.TrimSpace(c.Text)
			if s == "" {
				continue
			}
			filled++
			if len(s) > longest {
				longest = len(s)
			}
			for _, f := range strings.Fields(s) {
				tokens++
				if len([]rune(f)) == 1 {
					singles++
				}
			}
		}
	}
	if filled == 0 || longest > maxTableCellChars {
		return true
	}
	if tokens > 0 && float64(singles)/float64(tokens) > maxTableSingleCharFrac {
		return true
	}
	hdrSingles := 0
	for _, c := range t.Columns {
		if len([]rune(strings.TrimSpace(c.Name))) <= 1 {
			hdrSingles++
		}
	}
	if float64(hdrSingles)/float64(len(t.Columns)) > maxHeaderSingleCharFrac {
		return true
	}
	if tight, pairs := countTightJunctions(t); pairs > 0 &&
		float64(tight)/float64(pairs) > maxTightJunctionFrac {
		return true
	}
	return float64(filled)/float64(total) < minTableDensity
}

// countTightJunctions counts how many adjacent cell pairs in a row have
// no gutter between their glyphs, and how many pairs were examined.
//
// The measurement is geometric, not textual. For each pair it takes the
// rightmost glyph edge in the left cell and the leftmost in the right,
// and asks whether the space between them is wide enough to read as a
// column separator. Text content is deliberately not consulted: two real
// columns can hold text that ends and begins with letters ("Make & Model"
// beside "Chassis No.") without that meaning anything, which is why an
// earlier text-adjacency version of this test rejected genuine invoice
// tables.
//
// The gutter threshold scales with font size because a gap is only
// meaningful relative to the type it separates: 3pt is a wide gutter in
// 6pt type and no gutter at all in 24pt.
func countTightJunctions(t *pdf.Table) (tight, pairs int) {
	for _, r := range t.Rows {
		// Index by column so "adjacent" means adjacent columns, not
		// adjacent entries in a sparse cell slice.
		byCol := make([]*pdf.Cell, len(t.Columns))
		for i := range r.Cells {
			c := &r.Cells[i]
			if c.Column >= 0 && c.Column < len(byCol) {
				byCol[c.Column] = c
			}
		}
		for i := 0; i+1 < len(byCol); i++ {
			left, right := byCol[i], byCol[i+1]
			if left == nil || right == nil {
				continue
			}
			leftEnd, ok1 := maxSpanEnd(left.Spans)
			rightStart, fontSize, ok2 := minSpanStart(right.Spans)
			if !ok1 || !ok2 || fontSize <= 0 {
				continue
			}
			pairs++
			if rightStart-leftEnd < fontSize*minGutterFraction {
				tight++
			}
		}
	}
	return tight, pairs
}

// maxSpanEnd returns the rightmost glyph edge among spans.
func maxSpanEnd(spans []pdf.TextSpan) (float64, bool) {
	end, ok := 0.0, false
	for _, s := range spans {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		if !ok || s.EndX > end {
			end, ok = s.EndX, true
		}
	}
	return end, ok
}

// minSpanStart returns the leftmost glyph origin among spans, along with
// that span's font size — the scale the gutter is judged against.
func minSpanStart(spans []pdf.TextSpan) (x, fontSize float64, ok bool) {
	for _, s := range spans {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		if !ok || s.X < x {
			x, fontSize, ok = s.X, s.FontSize, true
		}
	}
	return x, fontSize, ok
}

// renderTable renders a detected table as GitHub-flavored Markdown.
//
// Cells are flattened and pipe-escaped: an unescaped pipe or newline in
// cell text would forge columns or end the row early, letting document
// content alter the table's apparent shape.
func renderTable(t *pdf.Table) string {
	if t == nil || len(t.Columns) == 0 {
		return ""
	}
	var b strings.Builder
	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = tableCell(c.Name)
	}
	fmt.Fprintf(&b, "| %s |\n|", strings.Join(header, " | "))
	for range t.Columns {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, r := range t.Rows {
		cells := make([]string, len(t.Columns))
		for _, c := range r.Cells {
			if c.Column >= 0 && c.Column < len(cells) {
				cells[c.Column] = tableCell(c.Text)
			}
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(cells, " | "))
	}
	return strings.TrimRight(b.String(), "\n")
}

func tableCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.Join(strings.Fields(s), " ")
}

// renderOutline renders the bookmark tree as an indented list. This is
// the cheapest structure a PDF offers: a few hundred bytes that say
// where everything is.
func renderOutline(items []pdf.OutlineItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Outline\n\n")
	for _, it := range items {
		b.WriteString(strings.Repeat("  ", it.Depth))
		b.WriteString("- ")
		b.WriteString(it.Title)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// metaPlaceholders are the /Info values that carry no signal — a
// producer's default rather than a real title or author. Emitting them
// makes the output lead with "# (anonymous)".
var metaPlaceholders = map[string]bool{
	"(anonymous)": true, "anonymous": true, "untitled": true,
	"(untitled)": true, "unknown": true, "(unknown)": true,
	"user": true, "(user)": true,
}

func metaPlaceholder(s string) bool {
	return metaPlaceholders[strings.ToLower(strings.TrimSpace(s))]
}

// headingRe promotes numbered-section lines to Markdown headings.
//
// Carried over from the Python filter, whose constraints were derived
// from running on real PRDs and specs: numeric prefix only (up to 4
// levels), a 60-char body cap, no internal periods (numbered *list*
// items are full prose with sentence punctuation; section headings are
// not), body must start uppercase, and no leading zeros in the number
// (ID-shaped table content like "03 the dashboard…" is the most common
// false positive).
var headingRe = regexp.MustCompile(`^([1-9]\d*(?:\.\d+){0,3})\.?\s+([A-Z][^.\n]{0,59})$`)

// promoteHeadings rewrites numbered-section lines as Markdown headings.
// Depth follows the numeric prefix, starting at H2 because the page
// already carries an H1-adjacent "## Page N" wrapper.
func promoteHeadings(text string) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		m := headingRe.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		depth := 2 + strings.Count(m[1], ".")
		if depth > 6 {
			depth = 6
		}
		lines[i] = strings.Repeat("#", depth) + " " + s
	}
	return strings.Join(lines, "\n")
}

// Repeated-furniture thresholds. A line on nearly every page is chrome,
// not content ("Cisco Confidential" on 34 of 35 pages), and emitting it
// 30 times is pure noise. Measured on that deck: furniture ran 0.66-0.97
// of pages, the highest genuine content line 0.11 — wide margin either
// side. Only short lines qualify (a repeated *paragraph* is boilerplate
// a reader may still want), and the 4-page floor exists because "on 60%
// of pages" in a 2-page document is one page, which is not a pattern.
const (
	furnitureMinPageFraction = 0.60
	furnitureMaxLineChars    = 120
	furnitureMinPages        = 4
)

// findFurniture returns the lines recurring on enough pages to be
// running headers/footers/navigation.
//
// Each line counts once per page, so a single busy page can't nominate
// its own repeated text as document furniture.
func findFurniture(pageTexts []string) map[string]bool {
	if len(pageTexts) < furnitureMinPages {
		return nil
	}
	counts := make(map[string]int)
	for _, text := range pageTexts {
		seen := make(map[string]bool)
		for _, line := range strings.Split(text, "\n") {
			s := strings.TrimSpace(line)
			if s == "" || len(s) > furnitureMaxLineChars || seen[s] {
				continue
			}
			seen[s] = true
			counts[s]++
		}
	}
	threshold := float64(len(pageTexts)) * furnitureMinPageFraction
	out := make(map[string]bool)
	for line, c := range counts {
		if float64(c) >= threshold {
			out[line] = true
		}
	}
	return out
}

// stripFurniture drops furniture lines, collapsing the runs of blank
// lines the removal leaves behind.
func stripFurniture(lines []string, furniture map[string]bool) string {
	if len(furniture) == 0 {
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	var out []string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if furniture[s] {
			continue
		}
		if s == "" && (len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// pageKind classifies what a page's extraction produced.
type pageKind int

const (
	pageBlank pageKind = iota
	pageText
	pageScanned
	pageChart
)

// minAlphaForText is the alphabetic-character floor below which a page
// with images is treated as a chart rather than text: extraction
// returned *something*, but too little to be prose — most likely axis
// labels around a figure.
const minAlphaForText = 20

// pageStrategy classifies a page from its extracted text and whether it
// carries images.
//
// The image signal is what separates a scan from a blank: no text and no
// image is an empty page, while no text plus a full-page image is a scan
// only OCR can read.
func pageStrategy(text string, hasImages bool) pageKind {
	s := strings.TrimSpace(text)
	if s == "" {
		if hasImages {
			return pageScanned
		}
		return pageBlank
	}
	alpha := 0
	for _, rr := range s {
		if (rr >= 'a' && rr <= 'z') || (rr >= 'A' && rr <= 'Z') || rr > 0x7F {
			alpha++
		}
	}
	if alpha < minAlphaForText && hasImages {
		return pageChart
	}
	return pageText
}
