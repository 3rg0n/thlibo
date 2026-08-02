package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// taggedPDF builds a Tagged PDF with a /H1, a /P and a 2x2 /Table.
//
// The fixture is built in code rather than checked in as a binary so the
// structure under test is readable: the /StructTreeRoot below is the
// whole point of these tests, and a reviewer must be able to see that
// MCID 2..5 really are the table cells.
//
// extraObjs/extraKids let individual tests graft on adversarial shapes
// (cycles, bogus MCIDs) without duplicating the whole builder.
func taggedPDF(t *testing.T, extraObjs map[int]string, extraKids string) []byte {
	t.Helper()

	content := "" +
		"BT /F1 18 Tf 72 720 Td /P <</MCID 0>> BDC (Quarterly Report) Tj EMC ET\n" +
		"BT /F1 11 Tf 72 690 Td /P <</MCID 1>> BDC (Revenue grew across all regions.) Tj EMC ET\n" +
		"BT /F1 11 Tf 72 650 Td /P <</MCID 2>> BDC (Region) Tj EMC ET\n" +
		"BT /F1 11 Tf 200 650 Td /P <</MCID 3>> BDC (Sales) Tj EMC ET\n" +
		"BT /F1 11 Tf 72 630 Td /P <</MCID 4>> BDC (EMEA) Tj EMC ET\n" +
		"BT /F1 11 Tf 200 630 Td /P <</MCID 5>> BDC (4210) Tj EMC ET\n"

	objs := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /MarkInfo << /Marked true >> " +
			"/StructTreeRoot 6 0 R /Lang (en-US) >>",
		2: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R /StructParents 0 >>",
		4: fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		5: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		6: "<< /Type /StructTreeRoot /K [7 0 R] >>",
		7: "<< /Type /StructElem /S /Document /P 6 0 R /K [8 0 R 9 0 R 10 0 R" +
			extraKids + "] >>",
		8:  "<< /Type /StructElem /S /H1 /P 7 0 R /Pg 3 0 R /K [0] >>",
		9:  "<< /Type /StructElem /S /P /P 7 0 R /Pg 3 0 R /K [1] >>",
		10: "<< /Type /StructElem /S /Table /P 7 0 R /Pg 3 0 R /K [11 0 R 12 0 R] >>",
		11: "<< /Type /StructElem /S /TR /P 10 0 R /Pg 3 0 R /K [13 0 R 14 0 R] >>",
		12: "<< /Type /StructElem /S /TR /P 10 0 R /Pg 3 0 R /K [15 0 R 16 0 R] >>",
		13: "<< /Type /StructElem /S /TH /P 11 0 R /Pg 3 0 R /K [2] >>",
		14: "<< /Type /StructElem /S /TH /P 11 0 R /Pg 3 0 R /K [3] >>",
		15: "<< /Type /StructElem /S /TD /P 12 0 R /Pg 3 0 R /K [4] >>",
		16: "<< /Type /StructElem /S /TD /P 12 0 R /Pg 3 0 R /K [5] >>",
	}
	for n, body := range extraObjs {
		objs[n] = body
	}
	return buildTaggedObjs(t, objs)
}

// buildTaggedObjs serialises a numbered object map into a PDF, wiring the
// xref by measured offset.
func buildTaggedObjs(t *testing.T, objs map[int]string) []byte {
	t.Helper()

	var buf strings.Builder
	buf.WriteString("%PDF-1.7\n")
	offsets := map[int]int{}
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
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		maxObj+1, xref)
	return []byte(buf.String())
}

func TestIsTaggedDetection(t *testing.T) {
	r, err := Open(taggedPDF(t, nil, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !r.IsTagged() {
		t.Fatal("tagged fixture reported as untagged")
	}

	// An ordinary generated PDF has no /MarkInfo and no tree.
	plain, err := Open(testPDF(t, "just some text"))
	if err != nil {
		t.Fatalf("open plain: %v", err)
	}
	if plain.IsTagged() {
		t.Error("untagged PDF reported as tagged")
	}
	if _, err := plain.StructuredMarkdown(); err == nil {
		t.Error("StructuredMarkdown must fail on an untagged PDF so the " +
			"caller falls through to geometry extraction")
	}
}

// TestStructuredMarkdown is the load-bearing test: geometry extraction
// finds ZERO tables in this document (FindTables returns nothing for it),
// while the tag tree states the table outright.
func TestStructuredMarkdown(t *testing.T) {
	r, err := Open(taggedPDF(t, nil, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}

	want := []string{
		"# Quarterly Report",
		"Revenue grew across all regions.",
		"| Region | Sales |",
		"| --- | --- |",
		"| EMEA | 4210 |",
	}
	for _, w := range want {
		if !strings.Contains(md, w) {
			t.Errorf("missing %q in:\n%s", w, md)
		}
	}

	// Heading must not also appear as a bare paragraph.
	if strings.Count(md, "Quarterly Report") != 1 {
		t.Errorf("heading emitted more than once:\n%s", md)
	}
}

// TestStructuredMarkdownBeatsGeometry pins the comparison that justifies
// this whole tier: the same document, both engines.
func TestStructuredMarkdownBeatsGeometry(t *testing.T) {
	raw := taggedPDF(t, nil, "")
	r, err := Open(raw)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	pages, err := r.Pages()
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	spans := ExtractPageText(pages[0], r)

	geo := FindTables(spans, nil)
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	structured := strings.Contains(md, "| EMEA | 4210 |")

	if !structured {
		t.Fatal("structure walk failed to recover the declared table")
	}
	t.Logf("geometry found %d table(s); structure walk recovered the table: %v",
		len(geo), structured)
}

// TestStructureTreeCycleTerminates: nothing in the format prevents a /K
// chain from pointing at an ancestor. Untagged input reaching a hook must
// never hang the process, so the walker tracks visited references.
func TestStructureTreeCycleTerminates(t *testing.T) {
	// Object 20 is a Sect whose child is object 20 — a self-cycle —
	// grafted onto the Document's kids.
	extra := map[int]string{
		20: "<< /Type /StructElem /S /Sect /P 7 0 R /K [20 0 R] >>",
	}
	r, err := Open(taggedPDF(t, extra, " 20 0 R"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = r.StructuredMarkdown()
		close(done)
	}()
	select {
	case <-done:
	case <-timeoutAfter():
		t.Fatal("StructuredMarkdown did not terminate on a cyclic tree")
	}
}

// TestStructureIgnoresUnbackedMCID is the security property: a structure
// element may only surface text that actually exists on the page under
// its own MCID. Here /TD claims MCID 999, which no span carries.
func TestStructureIgnoresUnbackedMCID(t *testing.T) {
	extra := map[int]string{
		16: "<< /Type /StructElem /S /TD /P 12 0 R /Pg 3 0 R /K [999] >>",
	}
	r, err := Open(taggedPDF(t, extra, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	// The cell must come back empty, and critically must NOT borrow the
	// text of some other MCID by falling back to document order.
	if strings.Contains(md, "| EMEA | 4210 |") {
		t.Errorf("unbacked MCID 999 resolved to real text:\n%s", md)
	}
	if !strings.Contains(md, "EMEA") {
		t.Errorf("sibling cell text lost:\n%s", md)
	}
}

// TestCellTextEscapesPipes: an unescaped pipe in cell content would forge
// columns in the rendered table.
func TestCellTextEscapesPipes(t *testing.T) {
	content := "BT /F1 11 Tf 72 650 Td /P <</MCID 0>> BDC (a|b) Tj EMC ET\n"
	objs := map[int]string{
		4:  fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		7:  "<< /Type /StructElem /S /Document /P 6 0 R /K [10 0 R] >>",
		10: "<< /Type /StructElem /S /Table /P 7 0 R /Pg 3 0 R /K [11 0 R] >>",
		11: "<< /Type /StructElem /S /TR /P 10 0 R /Pg 3 0 R /K [13 0 R] >>",
		13: "<< /Type /StructElem /S /TD /P 11 0 R /Pg 3 0 R /K [0] >>",
	}
	r, err := Open(taggedPDF(t, objs, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	if !strings.Contains(md, `a\|b`) {
		t.Errorf("pipe not escaped in cell text:\n%s", md)
	}
}

// twoPageTaggedPDF builds a tagged document where BOTH pages use MCID 0
// and MCID 1 — the normal case, since ids are only unique within a
// content stream. Page 1's /H1 is MCID 0, page 2's /P is also MCID 0.
func twoPageTaggedPDF(t *testing.T, pg2 string) []byte {
	t.Helper()

	c1 := "BT /F1 18 Tf 72 720 Td /H1 <</MCID 0>> BDC (Page One Heading) Tj EMC ET\n"
	c2 := "BT /F1 11 Tf 72 700 Td /P <</MCID 0>> BDC (Page two body text.) Tj EMC ET\n"

	objs := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /MarkInfo << /Marked true >> " +
			"/StructTreeRoot 6 0 R >>",
		2: "<< /Type /Pages /Kids [3 0 R 17 0 R] /Count 2 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		4: fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(c1), c1),
		5: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		6: "<< /Type /StructTreeRoot /K [7 0 R] >>",
		7: "<< /Type /StructElem /S /Document /P 6 0 R /K [8 0 R 9 0 R] >>",
		8: "<< /Type /StructElem /S /H1 /P 7 0 R /Pg 3 0 R /K [0] >>",
		9: "<< /Type /StructElem /S /P /P 7 0 R " + pg2 + " /K [0] >>",
		17: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 5 0 R >> >> /Contents 18 0 R >>",
		18: fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(c2), c2),
	}
	return buildTaggedObjs(t, objs)
}

// TestStructurePageScopedMCIDs is the regression test for the bug that
// keying text by MCID alone introduces: with a flat map, page 2's /P
// (MCID 0) resolves to page 1's heading text, because both pages number
// their marked content from 0.
func TestStructurePageScopedMCIDs(t *testing.T) {
	r, err := Open(twoPageTaggedPDF(t, "/Pg 17 0 R"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	if !strings.Contains(md, "# Page One Heading") {
		t.Errorf("page 1 heading missing:\n%s", md)
	}
	if !strings.Contains(md, "Page two body text.") {
		t.Errorf("page 2 body missing:\n%s", md)
	}
	// The failure mode: page 1's text surfacing a second time under
	// page 2's element.
	if strings.Count(md, "Page One Heading") != 1 {
		t.Errorf("page 1 text leaked into a page 2 element:\n%s", md)
	}
}

// TestStructureInheritsPageWhenPgAbsent: /Pg is optional, and an element
// without one takes the page of its nearest ancestor that has one. Here
// the page-2 element omits /Pg, so it inherits — and must NOT silently
// resolve against page 2's ids.
func TestStructureInheritsPageWhenPgAbsent(t *testing.T) {
	r, err := Open(twoPageTaggedPDF(t, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	// Inheriting from /Document (no /Pg) means page 1, where MCID 0 is
	// the heading. The element must resolve there, not against page 2.
	if !strings.Contains(md, "Page One Heading") {
		t.Errorf("inherited page did not resolve:\n%s", md)
	}
	if strings.Contains(md, "Page two body text.") {
		t.Errorf("element without /Pg resolved against the wrong page:\n%s", md)
	}
}

// TestStructureMCRReference: /K may name marked content through an /MCR
// dictionary rather than a bare integer, and the /MCR may redirect the
// page. Real corpus documents use both forms.
func TestStructureMCRReference(t *testing.T) {
	extra := map[int]string{
		// /H1 reaches MCID 0 via /MCR instead of the integer 0.
		8: "<< /Type /StructElem /S /H1 /P 7 0 R /Pg 3 0 R " +
			"/K [<< /Type /MCR /Pg 3 0 R /MCID 0 >>] >>",
	}
	r, err := Open(taggedPDF(t, extra, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	if !strings.Contains(md, "# Quarterly Report") {
		t.Errorf("/MCR reference did not resolve:\n%s", md)
	}
}

// TestStructureJoinsSplitWords is the quality regression: producers split
// one word across several show operators, so a block's spans must be
// rendered together through the same gap analysis the geometry path uses.
// Joining their text with a separator invents word breaks
// ("C loud Ap plicat ion"); concatenating bare welds words together.
func TestStructureJoinsSplitWords(t *testing.T) {
	// "Cloudy" emitted as three spans inside one /P, plus a genuinely
	// separate word after a real gap. Note Td is relative to the *line*
	// start, not the current point, so 200 places the second word well
	// clear of the first rather than 200pt further along.
	content := "BT /F1 12 Tf 72 700 Td /P <</MCID 0>> BDC (C) Tj (loud) Tj (y) Tj " +
		"200 0 Td (weather) Tj EMC ET\n"
	objs := map[int]string{
		4: fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		7: "<< /Type /StructElem /S /Document /P 6 0 R /K [9 0 R] >>",
		9: "<< /Type /StructElem /S /P /P 7 0 R /Pg 3 0 R /K [0] >>",
	}
	r, err := Open(taggedPDF(t, objs, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	if !strings.Contains(md, "Cloudy") {
		t.Errorf("split word not rejoined:\n%s", md)
	}
	if !strings.Contains(md, "Cloudy weather") {
		t.Errorf("real word gap not preserved:\n%s", md)
	}
}

// TestStructureNonStructCarriesText: /NonStruct is the standard wrapper
// for content with no semantic role. It is a grouping role, but it does
// carry text directly — dropping it loses real content. Measured on a
// 30-page tagged PDF, 1550 of 2050 elements were /NonStruct.
func TestStructureNonStructCarriesText(t *testing.T) {
	extra := map[int]string{
		9: "<< /Type /StructElem /S /NonStruct /P 7 0 R /Pg 3 0 R /K [1] >>",
	}
	r, err := Open(taggedPDF(t, extra, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	if !strings.Contains(md, "Revenue grew across all regions.") {
		t.Errorf("/NonStruct text dropped:\n%s", md)
	}
}

// TestStructureNestedSpanTextNotLost: a /P whose content sits in a child
// /Span must still emit that text, once.
func TestStructureNestedSpanTextNotLost(t *testing.T) {
	extra := map[int]string{
		// /P holds no ids itself; its /Span child holds MCID 1.
		9:  "<< /Type /StructElem /S /P /P 7 0 R /Pg 3 0 R /K [30 0 R] >>",
		30: "<< /Type /StructElem /S /Span /P 9 0 R /Pg 3 0 R /K [1] >>",
	}
	r, err := Open(taggedPDF(t, extra, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	if !strings.Contains(md, "Revenue grew across all regions.") {
		t.Errorf("nested /Span text lost:\n%s", md)
	}
	if strings.Count(md, "Revenue grew") != 1 {
		t.Errorf("nested text emitted more than once:\n%s", md)
	}
}

// TestStructureDropsSpacerColumns: authoring tools emit layout spacer
// cells that are empty in every row. They carry no data and cost a token
// per row downstream, so trailing all-empty columns are dropped.
func TestStructureDropsSpacerColumns(t *testing.T) {
	extra := map[int]string{
		// Give each row a third cell that no span backs.
		11: "<< /Type /StructElem /S /TR /P 10 0 R /Pg 3 0 R /K [13 0 R 14 0 R 40 0 R] >>",
		12: "<< /Type /StructElem /S /TR /P 10 0 R /Pg 3 0 R /K [15 0 R 16 0 R 41 0 R] >>",
		40: "<< /Type /StructElem /S /TD /P 11 0 R /Pg 3 0 R /K [900] >>",
		41: "<< /Type /StructElem /S /TD /P 12 0 R /Pg 3 0 R /K [901] >>",
	}
	r, err := Open(taggedPDF(t, extra, ""))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	md, err := r.StructuredMarkdown()
	if err != nil {
		t.Fatalf("StructuredMarkdown: %v", err)
	}
	if !strings.Contains(md, "| Region | Sales |") {
		t.Errorf("spacer column not dropped:\n%s", md)
	}
	if strings.Contains(md, "| --- | --- | --- |") {
		t.Errorf("separator still spans the dropped column:\n%s", md)
	}
}

func TestIsEncryptedRefusal(t *testing.T) {
	// Plain document: not encrypted.
	r, err := Open(testPDF(t, "hello"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if r.IsEncrypted() {
		t.Error("unencrypted PDF reported as encrypted")
	}
}
