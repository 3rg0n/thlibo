package pdf

// thlibo: local addition. Upstream gopdf has 102 tests and no fuzzing,
// which was defensible for its use case — generating and reading PDFs the
// caller authored. thlibo's is the opposite: every byte this package sees
// arrives from a file an AI client was told to read, so the parser is an
// untrusted-input boundary and the interesting failures are the ones no
// hand-written fixture thinks to write.
//
// What these targets assert is narrow on purpose: **no panic, no hang, no
// unbounded allocation.** Correct extraction from a corrupt document is
// not a thing to assert — there is no right answer. A panic, though, is a
// real defect even under the fail-open contract: RunNative recovers it and
// returns the input, so the client survives, but the crash-safety net is
// meant to be a backstop and not the mechanism. A hang has no net at all
// (there is no timeout inside this package), which makes the bounded-walk
// invariants the highest-value thing here.
//
// Run: go test ./internal/pdf/ -run Fuzz -fuzz FuzzOpenBytes -fuzztime 60s

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzOpenBytes drives the whole read path — xref parsing, object
// resolution, stream decoding, text extraction, tables, and the structure
// tree — against arbitrary bytes. This is the composite target: it is how
// pdfFilter actually uses the package, so a crash here is a crash in
// production.
func FuzzOpenBytes(f *testing.F) {
	f.Add(testPDF(f, "hello world"))
	f.Add(testMultiPagePDF(f, "page one", "page two"))
	f.Add([]byte("%PDF-1.7\n"))
	f.Add([]byte("%PDF-1.7\ntrailer\n<< /Root 1 0 R >>\nstartxref\n9\n%%EOF\n"))
	// A self-referential object: the shape that turns a resolver into an
	// infinite loop if it doesn't track what it has visited.
	f.Add([]byte("%PDF-1.7\n1 0 obj\n1 0 R\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n"))
	// Declared length far past the file: the classic over-read.
	f.Add([]byte("%PDF-1.7\n1 0 obj\n<< /Length 999999 >>\nstream\nshort\nendstream\nendobj\n%%EOF\n"))
	// An inline image, which is the one place the content-stream tokenizer
	// has to skip over arbitrary binary rather than parse it. A scan that
	// stops short leaves the lexer reading pixel bytes as operators.
	f.Add(inlineImagePDF(f, "BI /W 2 /H 2 /BPC 8 /CS /G ID \x00\x01\x02\x03 EI"))
	// A declared /L that lands inside the data rather than on EI: the
	// file-controlled length must be verified, not trusted.
	f.Add(inlineImagePDF(f, "BI /W 2 /H 2 /BPC 8 /CS /G /L 1 ID \x00\x01\x02\x03 EI"))
	// A Form XObject naming itself: the fan-out shape the visited set bounds.
	f.Add(selfReferentialFormPDF(f))

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := OpenBytes(data)
		if err != nil || doc == nil {
			return
		}
		// A page count is derived from file contents, so a corrupt document
		// can claim an enormous one. Bound the walk here rather than trusting
		// it — an unbounded loop over a fabricated count is a hang, and the
		// fuzzer would report it as a timeout with no useful minimisation.
		n := doc.NumPages()
		if n > 8 {
			n = 8
		}
		for i := 0; i < n; i++ {
			p := doc.Page(i)
			if p == nil {
				continue
			}
			spans, _ := p.TextSpans()
			_, _ = p.TextLines()
			_ = p.HasImages()
			// Images walks the content stream and follows Form XObjects into
			// file-controlled /Resources, so it reaches more untrusted
			// structure than HasImages ever did. Decode the images too: the
			// sample decoder sizes a row stride from declared geometry, and a
			// fabricated one must error rather than over-read.
			for _, im := range p.Images() {
				_, _ = im.Decode()
			}
			if len(spans) > 0 && len(spans) < 4000 {
				_ = FindTables(spans, nil)
			}
		}
		_ = doc.InfoString("Title")
		_ = doc.Outline()
		if doc.IsTagged() {
			_, _ = doc.StructuredMarkdown()
		}
	})
}

// FuzzParseInlineDict targets the BDC properties dictionary parser
// specifically. It is thlibo's own addition (upstream skipped inline
// dicts entirely) and it recurses, which is the combination worth
// fuzzing: the depth cap is the only thing between a nested dict and a
// stack overflow, and a stack overflow is the one failure mode Go's
// recover() cannot catch.
func FuzzParseInlineDict(f *testing.F) {
	f.Add("/MCID 3 /Type /Pagination >>")
	f.Add("/Nested << /MCID 1 >> /MCID 2 >>")
	f.Add(">>")
	f.Add("")
	// Unterminated, so the parser has to stop on EOF rather than on a
	// closing token.
	f.Add("/MCID 1 /More")
	// Deeper than the depth cap, which is the invariant under test.
	f.Add(strings.Repeat("<< /A ", 200) + "1" + strings.Repeat(" >>", 200))

	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 1<<16 {
			t.Skip("oversized input says nothing new about the depth cap")
		}
		d := parseInlineDict(NewLexer([]byte(s)), 0)
		// The result is used as marked-content properties, so the only
		// contract is that it is a usable map — not what is in it.
		_ = len(d)
	})
}

// inlineImagePDF wraps a content stream containing an inline image into a
// one-page document, so the seed reaches the walker through OpenBytes rather
// than through the lexer directly.
func inlineImagePDF(t testing.TB, content string) []byte {
	return buildImagePDF(t, content, nil)
}

// selfReferentialFormPDF builds the fan-out shape: a Form XObject whose own
// /Resources name it twice, which expands exponentially with depth if the
// visited set is not doing its job.
func selfReferentialFormPDF(t testing.TB) []byte {
	inner := "/Fm0 Do /Fm0 Do"
	form := fmt.Sprintf("<< /Type /XObject /Subtype /Form "+
		"/Resources << /XObject << /Fm0 5 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
		len(inner), inner)
	return buildImagePDF(t, "/Fm0 Do", map[string]string{"Fm0": form})
}

// FuzzDecodeTextString targets metadata and outline decoding. Also a
// thlibo addition, and the one place where document bytes become *model-
// facing text* rather than an internal structure: a title goes into the
// output as an H1. It must therefore never return control characters,
// whatever the input claims to be encoded as.
func FuzzDecodeTextString(f *testing.F) {
	f.Add("plain ascii title")
	f.Add("\xfe\xff\x00T\x00i\x00t\x00l\x00e") // UTF-16BE with BOM
	f.Add("\xfe\xff\x00")                      // BOM then an odd trailing byte
	f.Add("caf\xe9 latin-1")
	f.Add("tab\there\nand a newline")
	f.Add("\x00\x01\x02\x1f\x7f")
	f.Add("")

	f.Fuzz(func(t *testing.T, s string) {
		got := sanitizeMetaText(DecodeTextString(s))
		for _, r := range got {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("control character %#x survived sanitising: %q -> %q", r, s, got)
			}
		}
		if strings.Contains(got, "  ") {
			t.Fatalf("runs of spaces were not collapsed: %q -> %q", s, got)
		}
	})
}
