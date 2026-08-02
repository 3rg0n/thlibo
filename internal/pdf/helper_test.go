package pdf

// thlibo: local addition, replacing upstream's creator_test.go helpers.
//
// Upstream builds test fixtures with NewCreator() — its PDF *generation*
// API, which we deliberately did not vendor (thlibo only ever reads).
// Rather than ship ~1,000 lines of writer/creator in the binary purely
// to construct test input, these helpers assemble the same minimal
// single- and multi-page documents by hand.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// buildPDF assembles a PDF from page content streams, wiring the xref
// table by measured offset. Deliberately literal: a fixture builder that
// shares code with the parser under test can hide bugs in both.
func buildPDF(t testing.TB, contents []string) []byte {
	t.Helper()

	const fontObj = 3
	// Object numbering: 1=Catalog, 2=Pages, 3=Font, then per page a
	// Page object and a Contents stream.
	firstPage := 4
	var kids []string
	for i := range contents {
		kids = append(kids, fmt.Sprintf("%d 0 R", firstPage+i*2))
	}

	objs := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R >>",
		2: fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
			strings.Join(kids, " "), len(contents)),
		fontObj: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i, c := range contents {
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
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		maxObj+1, xref)

	return []byte(buf.String())
}

// textStream renders lines as a content stream, one line per 16pt step —
// matching the layout upstream's creator produced.
func textStream(lines []string) string {
	var b strings.Builder
	y := 750.0
	for _, ln := range lines {
		fmt.Fprintf(&b, "BT /F1 12 Tf 72 %.0f Td (%s) Tj ET\n", y, escapePDFString(ln))
		y -= 16
	}
	return b.String()
}

// escapePDFString escapes the three characters that are special inside a
// PDF literal string.
func escapePDFString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "(", `\(`)
	s = strings.ReplaceAll(s, ")", `\)`)
	return s
}

// testPDF creates a single-page PDF with the given text lines.
func testPDF(t testing.TB, lines ...string) []byte {
	t.Helper()
	return buildPDF(t, []string{textStream(lines)})
}

// testMultiPagePDF creates a PDF with one page per supplied string.
func testMultiPagePDF(t testing.TB, pageTexts ...string) []byte {
	t.Helper()
	contents := make([]string, 0, len(pageTexts))
	for _, txt := range pageTexts {
		contents = append(contents, textStream([]string{txt}))
	}
	return buildPDF(t, contents)
}

// timeoutAfter bounds a test that must prove termination. Deliberately
// short: the walker either stops immediately or it never does.
func timeoutAfter() <-chan time.Time {
	return time.After(10 * time.Second)
}
