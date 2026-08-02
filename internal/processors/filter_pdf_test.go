package processors

import (
	"fmt"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/pdf"
)

// pdf-filter tests. Two layers, because the filter has two kinds of
// logic and they fail differently:
//
//   - End-to-end through pdfFilter, on hand-built PDF bytes. This is the
//     only way to test the fail-open contract, since what makes it a
//     contract is that a *malformed document* comes back unchanged.
//   - Direct unit tests on the classification and rendering helpers,
//     which take spans/tables/text rather than a document. Constructing a
//     PDF whose glyph geometry exercises a specific isLayoutGrid branch
//     would test the table detector more than the guard.
//
// The fixture builder is deliberately literal for the same reason
// internal/pdf's is: a builder that shares code with the parser under
// test can hide a bug in both.

// buildTestPDF assembles a minimal PDF from per-page content streams,
// wiring the xref by measured offset. extraTrailer is appended to the
// trailer dictionary (used to inject /Encrypt).
func buildTestPDF(pages []string, extraTrailer string) []byte {
	const fontObj = 3
	firstPage := 4

	var kids []string
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", firstPage+i*2))
	}
	objs := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R >>",
		2: fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
			strings.Join(kids, " "), len(pages)),
		fontObj: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i, c := range pages {
		pageNum := firstPage + i*2
		streamNum := pageNum + 1
		objs[pageNum] = fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
				"/Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			fontObj, streamNum)
		objs[streamNum] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(c), c)
	}

	var buf strings.Builder
	buf.WriteString("%PDF-1.7\n")
	offsets := make(map[int]int)
	maxObj := 0
	for n := range objs {
		if n > maxObj {
			maxObj = n
		}
	}
	for n := 1; n <= maxObj; n++ {
		body, ok := objs[n]
		if !ok {
			continue
		}
		offsets[n] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", maxObj+1)
	buf.WriteString("0000000000 65535 f \n")
	for n := 1; n <= maxObj; n++ {
		if off, ok := offsets[n]; ok {
			fmt.Fprintf(&buf, "%010d 00000 n \n", off)
		} else {
			buf.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R %s>>\nstartxref\n%d\n%%%%EOF\n",
		maxObj+1, extraTrailer, xref)
	return []byte(buf.String())
}

// pdfTextStream lays lines out one 16pt step apart, the same spacing
// internal/pdf's fixtures use.
func pdfTextStream(lines ...string) string {
	var b strings.Builder
	y := 750.0
	for _, ln := range lines {
		esc := strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(ln)
		fmt.Fprintf(&b, "BT /F1 12 Tf 72 %.0f Td (%s) Tj ET\n", y, esc)
		y -= 16
	}
	return b.String()
}

// pdfWithPages builds a PDF where each argument is one page's text.
func pdfWithPages(pageLines ...[]string) []byte {
	streams := make([]string, 0, len(pageLines))
	for _, lines := range pageLines {
		streams = append(streams, pdfTextStream(lines...))
	}
	return buildTestPDF(streams, "")
}

func TestPDFFilterExtractsText(t *testing.T) {
	in := pdfWithPages([]string{"Quarterly Report", "Revenue rose sharply this period."})
	out := string(pdfFilter(in))

	for _, want := range []string{"## Page 1", "Quarterly Report", "Revenue rose sharply"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(out, "- pages: 1") {
		t.Errorf("output missing page count\n---\n%s", out)
	}
}

// A filter's return value IS the tool output, so every failure path has
// to return the input verbatim rather than a diagnostic (invariant #2).
func TestPDFFilterFailsOpen(t *testing.T) {
	valid := pdfWithPages([]string{"Some ordinary body text goes here."})

	cases := []struct {
		name string
		in   []byte
	}{
		{"not a pdf", []byte("plain text, no signature anywhere in here")},
		{"signature too deep", append(make([]byte, 2048), []byte("%PDF-1.7\n")...)},
		{"truncated pdf", valid[:len(valid)/2]},
		{"header only", []byte("%PDF-1.7\n")},
		{"empty", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := pdfFilter(c.in)
			if string(out) != string(c.in) {
				t.Errorf("input was rewritten (%d bytes in, %d out):\n%.200q",
					len(c.in), len(out), out)
			}
		})
	}
}

// An encrypted document parses fine and extracts ciphertext, with no
// error anywhere to key off — so the /Encrypt check is the only thing
// standing between the caller and plausible-looking garbage.
func TestPDFFilterRefusesEncrypted(t *testing.T) {
	stream := pdfTextStream("Confidential figures follow on the next page.")
	in := buildTestPDF([]string{stream}, "/Encrypt << /Filter /Standard /V 2 /R 3 /Length 128 >> ")

	// Guard the fixture: if the reader doesn't see the /Encrypt, this test
	// would pass for the wrong reason.
	doc, err := pdf.OpenBytes(in)
	if err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	if !doc.IsEncrypted() {
		t.Fatal("fixture is not detected as encrypted; test would be vacuous")
	}

	if out := pdfFilter(in); string(out) != string(in) {
		t.Errorf("encrypted document was processed instead of passed through:\n%.300q", out)
	}
}

// Running headers/footers are identifiable only document-wide, and only
// once enough pages agree.
func TestFindFurniture(t *testing.T) {
	pages := make([]string, 5)
	for i := range pages {
		pages[i] = "ACME Confidential\nBody text unique to page " +
			string(rune('A'+i)) + "\nPage footer 1 of 5"
	}
	f := findFurniture(pages)

	for _, want := range []string{"ACME Confidential", "Page footer 1 of 5"} {
		if !f[want] {
			t.Errorf("%q not identified as furniture (got %v)", want, f)
		}
	}
	if f["Body text unique to page A"] {
		t.Error("unique body line wrongly identified as furniture")
	}
}

// "On 60% of pages" in a short document is one or two pages, which is a
// coincidence rather than a pattern.
func TestFindFurnitureIgnoresShortDocuments(t *testing.T) {
	pages := []string{"Header\nalpha", "Header\nbeta", "Header\ngamma"}
	if f := findFurniture(pages); len(f) != 0 {
		t.Errorf("furniture detected in a %d-page document: %v", len(pages), f)
	}
}

// A repeated *paragraph* is boilerplate a reader may still want, so only
// short lines qualify.
func TestFindFurnitureIgnoresLongLines(t *testing.T) {
	long := strings.Repeat("long boilerplate clause ", 10) // > 120 chars
	pages := make([]string, 5)
	for i := range pages {
		pages[i] = long + "\nshort"
	}
	f := findFurniture(pages)
	if f[long] {
		t.Error("long repeated line wrongly identified as furniture")
	}
	if !f["short"] {
		t.Error("short repeated line should still be furniture")
	}
}

func TestStripFurnitureCollapsesBlanks(t *testing.T) {
	lines := []string{"Header", "", "real content", "", "Footer", "", "more content"}
	got := stripFurniture(lines, map[string]bool{"Header": true, "Footer": true})
	want := "real content\n\nmore content"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPromoteHeadings(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1 Introduction", "## 1 Introduction"},
		{"2.1 Scope And Intent", "### 2.1 Scope And Intent"},
		{"3.2.1 Deeper Section", "#### 3.2.1 Deeper Section"},
		{"1.2.3.4 Deepest Section", "##### 1.2.3.4 Deepest Section"},
		// Numbered list items are prose with sentence punctuation; section
		// headings are not.
		{"1 This is a sentence. And another.", "1 This is a sentence. And another."},
		// Leading zeros mean an identifier, not a section number — the most
		// common false positive is ID-shaped table content.
		{"03 The Dashboard", "03 The Dashboard"},
		// A heading names something; lowercase means mid-sentence text.
		{"4 lowercase start", "4 lowercase start"},
		{"Plain prose line", "Plain prose line"},
		{"", ""},
	}
	for _, c := range cases {
		if got := promoteHeadings(c.in); got != c.want {
			t.Errorf("promoteHeadings(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Depth is capped because Markdown has no H7.
func TestPromoteHeadingsCapsDepth(t *testing.T) {
	got := promoteHeadings("1.2.3.4 Section Title")
	if !strings.HasPrefix(got, "##### ") || strings.HasPrefix(got, "###### ") {
		t.Errorf("unexpected depth: %q", got)
	}
}

func TestPageStrategy(t *testing.T) {
	prose := "This page carries several sentences of ordinary readable prose."
	cases := []struct {
		name   string
		text   string
		images bool
		want   pageKind
	}{
		{"text", prose, false, pageText},
		{"text with images", prose, true, pageText},
		// No text plus a full-page image is a scan; no text and no image is
		// simply an empty page. The image signal is the whole distinction.
		{"scanned", "  \n\n ", true, pageScanned},
		{"blank", "", false, pageBlank},
		// Extraction returned something, but too little to be prose — axis
		// labels around a figure.
		{"chart", "0 5 10 15 20 kWh", true, pageChart},
		{"sparse text no images", "0 5 10 15 20 kWh", false, pageText},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pageStrategy(c.text, c.images); got != c.want {
				t.Errorf("pageStrategy(%q, %v) = %v, want %v", c.text, c.images, got, c.want)
			}
		})
	}
}

// A document with pages but no extractable text is a scan, and only the
// vision OCR path can read it — which is what the sentinel triggers.
func TestPDFFilterEmitsLowValueSentinel(t *testing.T) {
	// A page whose content stream draws nothing: no text, no image.
	in := buildTestPDF([]string{"q Q\n"}, "")
	out := string(pdfFilter(in))

	if !strings.Contains(out, LowValueSentinel) {
		t.Errorf("no sentinel for a text-free document\n---\n%s", out)
	}
	if !strings.Contains(out, "_[blank page]_") {
		t.Errorf("missing blank-page placeholder\n---\n%s", out)
	}
}

func TestPDFFilterNoSentinelWhenTextFound(t *testing.T) {
	in := pdfWithPages([]string{"Ordinary readable body text on this page."})
	if out := string(pdfFilter(in)); strings.Contains(out, LowValueSentinel) {
		t.Errorf("sentinel emitted for a document with text\n---\n%s", out)
	}
}

// The page cap has to announce itself: silently emitting 200 of 260 pages
// reads as a short document rather than a truncated one.
func TestPDFFilterTruncatesAtPageCap(t *testing.T) {
	total := maxPDFPages + 12
	pages := make([][]string, total)
	for i := range pages {
		pages[i] = []string{fmt.Sprintf("Page body number %d with some prose on it.", i+1)}
	}
	out := string(pdfFilter(pdfWithPages(pages...)))

	want := fmt.Sprintf("_[document truncated: %d of %d pages rendered]_", maxPDFPages, total)
	if !strings.Contains(out, want) {
		t.Errorf("missing truncation note %q\n---\n%.400s", want, out)
	}
	if strings.Contains(out, fmt.Sprintf("## Page %d", maxPDFPages+1)) {
		t.Errorf("rendered past the page cap")
	}
	if !strings.Contains(out, fmt.Sprintf("## Page %d", maxPDFPages)) {
		t.Errorf("stopped short of the page cap")
	}
}

// span is shorthand for a positioned glyph run in the table fixtures
// below. x2 is the right edge, which is what the gutter measurement uses.
func span(text string, x, x2 float64) pdf.TextSpan {
	return pdf.TextSpan{X: x, EndX: x2, FontSize: 10, Text: text, MCID: -1}
}

// tableOf builds a Table from column names and rows of (text, x, endX)
// triples, so a test can state the geometry it means to exercise.
func tableOf(cols []string, rows ...[]pdf.TextSpan) *pdf.Table {
	t := &pdf.Table{}
	for i, name := range cols {
		t.Columns = append(t.Columns, pdf.Column{Name: name, X: float64(i) * 100})
	}
	for ri, spans := range rows {
		row := pdf.Row{Y: 700 - float64(ri)*14}
		for ci, s := range spans {
			row.Cells = append(row.Cells, pdf.Cell{
				Column: ci, Text: s.Text, Spans: []pdf.TextSpan{s},
			})
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func TestIsLayoutGridAcceptsRealTable(t *testing.T) {
	// Three columns with real gutters between them: ~90pt of space at
	// 10pt type.
	tbl := tableOf([]string{"Region", "Units", "Revenue"},
		[]pdf.TextSpan{span("North", 0, 40), span("1200", 100, 130), span("48,000", 200, 240)},
		[]pdf.TextSpan{span("South", 0, 40), span("980", 100, 125), span("39,200", 200, 240)},
		[]pdf.TextSpan{span("West", 0, 35), span("1410", 100, 130), span("56,400", 200, 240)},
	)
	if isLayoutGrid(tbl) {
		t.Error("a real table with proper gutters was rejected")
	}
}

func TestIsLayoutGridRejects(t *testing.T) {
	wide := strings.Repeat("paragraph text ", 100) // > maxTableCellChars

	cases := []struct {
		name  string
		table *pdf.Table
	}{
		{"nil", nil},
		{"single column", tableOf([]string{"Only"},
			[]pdf.TextSpan{span("a", 0, 10)},
			[]pdf.TextSpan{span("b", 0, 10)})},
		// One data row carries no row/column relationship — it is a line of
		// text with wide gaps, and no geometry can tell the difference.
		{"one row", tableOf([]string{"A", "B"},
			[]pdf.TextSpan{span("x", 0, 10), span("y", 100, 110)})},
		// A grid whose cells hold whole paragraphs is a slide-deck layout
		// frame; the text is already in the page body.
		{"paragraph cells", tableOf([]string{"Left", "Right"},
			[]pdf.TextSpan{span(wide, 0, 300), span(wide, 400, 700)},
			[]pdf.TextSpan{span(wide, 0, 300), span(wide, 400, 700)})},
		// Letter-shredded glyph runs: gap analysis split a word, not a row.
		{"shredded tokens", tableOf([]string{"Alpha", "Beta"},
			[]pdf.TextSpan{span("c r y p t l i b", 0, 60), span("s o f t w a r e", 100, 160)},
			[]pdf.TextSpan{span("m a n u a l", 0, 50), span("v e r s i o n", 100, 155)})},
		// Single-character column names mean the boundaries landed mid-word,
		// which is proof the columns aren't real.
		{"single-char headers", tableOf([]string{"t", "R", "r"},
			[]pdf.TextSpan{span("some words here", 0, 80), span("more words", 100, 170), span("and more", 200, 260)},
			[]pdf.TextSpan{span("further text", 0, 80), span("continuing on", 100, 175), span("to the end", 200, 265)})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !isLayoutGrid(c.table) {
				t.Error("not rejected")
			}
		})
	}
}

// The gutter test is the one guard that measures evidence rather than a
// shape: a column boundary is *space*, so glyphs that nearly touch were
// never separated by one.
func TestIsLayoutGridRejectsTightJunctions(t *testing.T) {
	// Each "column" begins 1pt after the previous one ends — 0.1 em at
	// 10pt type, far below any inter-word space, let alone a gutter.
	tight := tableOf([]string{"Introduction", "to the topic"},
		[]pdf.TextSpan{span("We have ca", 0, 60), span("rr ied out a", 61, 130)},
		[]pdf.TextSpan{span("Visual Hea", 0, 60), span("lt h Inspection", 61, 135)},
		[]pdf.TextSpan{span("on your ve", 0, 60), span("hi cle.", 61, 100)},
	)
	if !isLayoutGrid(tight) {
		tj, pairs := countTightJunctions(tight)
		t.Errorf("prose split at tight junctions was accepted (tight=%d/%d)", tj, pairs)
	}

	// The same cell text with real gutters is a table, which proves the
	// verdict comes from the geometry rather than the words.
	loose := tableOf([]string{"Introduction", "to the topic"},
		[]pdf.TextSpan{span("We have ca", 0, 60), span("rr ied out a", 200, 270)},
		[]pdf.TextSpan{span("Visual Hea", 0, 60), span("lt h Inspection", 200, 275)},
		[]pdf.TextSpan{span("on your ve", 0, 60), span("hi cle.", 200, 240)},
	)
	if isLayoutGrid(loose) {
		t.Error("same text with real gutters was rejected; the guard is reading words, not geometry")
	}
}

func TestCountTightJunctionsScalesWithFontSize(t *testing.T) {
	// A 3pt gap: a real gutter in 6pt type, no gutter at all in 24pt.
	build := func(fs float64) *pdf.Table {
		l := pdf.TextSpan{X: 0, EndX: 50, FontSize: fs, Text: "left", MCID: -1}
		r := pdf.TextSpan{X: 53, EndX: 100, FontSize: fs, Text: "right", MCID: -1}
		return tableOf([]string{"A", "B"}, []pdf.TextSpan{l, r})
	}
	if tj, pairs := countTightJunctions(build(6)); tj != 0 || pairs != 1 {
		t.Errorf("6pt type: tight=%d/%d, want 0/1", tj, pairs)
	}
	if tj, pairs := countTightJunctions(build(24)); tj != 1 || pairs != 1 {
		t.Errorf("24pt type: tight=%d/%d, want 1/1", tj, pairs)
	}
}

// An empty cell says nothing about whether the boundary beside it is
// real, so it must not be counted either way.
func TestCountTightJunctionsSkipsEmptyCells(t *testing.T) {
	tbl := tableOf([]string{"A", "B", "C"},
		[]pdf.TextSpan{span("left", 0, 40), span("", 50, 50), span("right", 200, 240)},
	)
	// Blank the middle cell's span text so it contributes no geometry.
	tbl.Rows[0].Cells[1].Spans = []pdf.TextSpan{{X: 50, EndX: 50, FontSize: 10, Text: "   ", MCID: -1}}
	if _, pairs := countTightJunctions(tbl); pairs != 0 {
		t.Errorf("pairs = %d, want 0 (both junctions touch the empty cell)", pairs)
	}
}

// Cell text is document content, so it must not be able to forge columns
// or end the row early.
func TestRenderTableEscapesCellContent(t *testing.T) {
	tbl := tableOf([]string{"Name", "Notes"},
		[]pdf.TextSpan{span("a|b", 0, 30), span("line one\nline two", 100, 200)},
		[]pdf.TextSpan{span("c", 0, 20), span("plain", 100, 140)},
	)
	got := renderTable(tbl)

	if strings.Contains(got, "a|b") {
		t.Errorf("unescaped pipe survived:\n%s", got)
	}
	if !strings.Contains(got, `a\|b`) {
		t.Errorf("pipe not escaped:\n%s", got)
	}
	if !strings.Contains(got, "line one line two") {
		t.Errorf("newline not flattened:\n%s", got)
	}
	// Header + separator + 2 data rows, and no row split by the newline.
	if n := len(strings.Split(got, "\n")); n != 4 {
		t.Errorf("got %d lines, want 4:\n%s", n, got)
	}
}

func TestRenderOutline(t *testing.T) {
	items := []pdf.OutlineItem{
		{Title: "Chapter 1", Depth: 0},
		{Title: "Section 1.1", Depth: 1},
		{Title: "Chapter 2", Depth: 0},
	}
	got := renderOutline(items)

	if !strings.HasPrefix(got, "## Outline") {
		t.Errorf("missing heading:\n%s", got)
	}
	if !strings.Contains(got, "\n  - Section 1.1") {
		t.Errorf("depth not indented:\n%s", got)
	}
	if renderOutline(nil) != "" {
		t.Error("empty outline should render nothing")
	}
}

// A producer's default is not a title; emitting it makes the output lead
// with "# (anonymous)".
func TestMetaPlaceholder(t *testing.T) {
	for _, s := range []string{"(anonymous)", "Anonymous", " untitled ", "UNKNOWN", "user"} {
		if !metaPlaceholder(s) {
			t.Errorf("%q not treated as a placeholder", s)
		}
	}
	for _, s := range []string{"Quarterly Report", "A. User", "Unknown Unknowns"} {
		if metaPlaceholder(s) {
			t.Errorf("%q wrongly treated as a placeholder", s)
		}
	}
}

// The registration is what makes the filter reachable at all: without it
// the descriptor resolves to a native processor with no implementation.
func TestPDFFilterIsRegistered(t *testing.T) {
	if nativeFilter("pdf-filter") == nil {
		t.Fatal("pdf-filter is not in the native registry")
	}
	in := pdfWithPages([]string{"Body text long enough to be worth extracting from."})
	out, handled := RunNative("pdf-filter", in)
	if !handled {
		t.Fatal("RunNative did not handle pdf-filter")
	}
	if !strings.Contains(string(out), "Body text long enough") {
		t.Errorf("unexpected output:\n%s", out)
	}
}
