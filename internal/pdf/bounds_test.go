package pdf

// thlibo: local addition. Termination tests for the three file-controlled
// walks in the read path — all three found by the fuzz targets in
// fuzz_test.go, and all three hangs rather than crashes.
//
// The distinction matters under thlibo's fail-open contract: RunNative
// recovers a panic and passes the original bytes through, so a crash
// degrades to "no compression". A hang has no such net — the hook blocks
// and the AI client waits on it, which is the one failure mode the
// contract cannot absorb. That is why these assert termination and say
// almost nothing about what was extracted: for a corrupt document there is
// no right answer, only a bounded wrong one.
//
// The failing inputs also live in testdata/fuzz/ and replay on every
// `go test`, but a regressed hang there surfaces as a package-wide timeout
// with no indication of which walk looped. These name the three walks so a
// failure says what broke.

import (
	"fmt"
	"strings"
	"testing"
)

// mustTerminate runs fn on a goroutine and fails if it outlives the bound.
// The goroutine is leaked on failure — the test binary is going down anyway,
// and there is no way to interrupt a spinning parser.
func mustTerminate(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-timeoutAfter():
		t.Fatalf("%s did not terminate", what)
	}
}

// readEverything exercises the paths a PDF processor actually takes, so a
// bound that only holds for Open but not for the page walk still fails.
func readEverything(data []byte) {
	doc, err := OpenBytes(data)
	if err != nil {
		return
	}
	for i := 0; i < doc.NumPages() && i < 8; i++ {
		if p := doc.Page(i); p != nil {
			_, _ = p.TextLines()
		}
	}
}

// TestXRefSubsectionCountIsNotTrusted is the regression test for the hang
// FuzzOpenBytes found on an xref subsection header of "0 60000000000".
//
// The entry count in that header is a claim from the file, and NextToken
// returns (TEOF, nil) rather than an error at end of input — so a loop that
// trusts the count does not stop when the entries run out. It performs its
// declared number of no-op iterations: sixty billion, in the minimised case.
func TestXRefSubsectionCountIsNotTrusted(t *testing.T) {
	body := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	xrefAt := len("%PDF-1.7\n") + len(body)
	in := "%PDF-1.7\n" + body +
		// One real entry, a claim of sixty billion.
		"xref\n0 60000000000\n0000000000 65535 f \n" +
		fmt.Sprintf("trailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefAt)

	mustTerminate(t, "xref subsection walk (declared entry count trusted?)", func() {
		readEverything([]byte(in))
	})
}

// TestXRefPrevCycleTerminates covers the defect adjacent to the one above:
// /Prev is a file-controlled offset that both xref readers follow by
// recursing, so a chain pointing back at itself recursed until the stack
// gave out.
func TestXRefPrevCycleTerminates(t *testing.T) {
	body := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	xrefAt := len("%PDF-1.7\n") + len(body)
	in := "%PDF-1.7\n" + body +
		"xref\n0 1\n0000000000 65535 f \n" +
		// /Prev names this very xref section.
		fmt.Sprintf("trailer\n<< /Size 3 /Root 1 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n",
			xrefAt, xrefAt)

	mustTerminate(t, "xref /Prev chain (cycle guard holding?)", func() {
		readEverything([]byte(in))
	})
}

// TestPageTreeCycleTerminates is the regression test for the hang
// FuzzOpenBytes found on a /Pages node whose /Kids names itself. /Kids can
// reference any object in the file, including its own parent, and the walk
// recursed straight through it.
func TestPageTreeCycleTerminates(t *testing.T) {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		// Object 2 is its own child.
		"<< /Type /Pages /Kids [2 0 R] /Count 1 >>",
	}

	var b strings.Builder
	b.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, xref)

	pages := -1
	mustTerminate(t, "page tree walk (cycle guard holding?)", func() {
		if doc, err := OpenBytes([]byte(b.String())); err == nil {
			pages = doc.NumPages()
		} else {
			pages = 0
		}
	})

	// A self-referential /Pages node contains no /Page, so the honest answer
	// is zero — not a page list padded out by re-walking the cycle.
	if pages != 0 {
		t.Errorf("collected %d pages from a self-referential page tree, want 0", pages)
	}
}

// TestFontEncodingDropsOutOfRangeDifferences: a /Differences array is a
// list of (code, glyph-name...) pairs where the code is a file-controlled
// number. Upstream narrowed it with byte(v), so /Differences [389 /alpha]
// aliased onto 0x85 and overwrote whichever glyph legitimately sat there —
// a silent text corruption rather than a crash, which is why it survived
// 102 upstream tests.
//
// Measured against the 41-document corpus: no real PDF reaches this
// branch, so the fix changes output only on malformed input.
func TestFontEncodingDropsOutOfRangeDifferences(t *testing.T) {
	r := &Reader{}
	// 389 truncates to 0x85 (ellipsis) and 390 to 0x86 (dagger) — both
	// carry a real WinAnsi glyph, so aliasing is observable.
	enc := r.FontEncoding(Dict{"Encoding": Dict{
		"BaseEncoding": Name("WinAnsiEncoding"),
		"Differences":  Array{389, Name("alpha"), Name("beta")},
	}})
	if got := enc[0x85]; got != "ellipsis" {
		t.Errorf("code 0x85 = %q, want %q: an out-of-range /Differences code aliased onto it", got, "ellipsis")
	}
	if got := enc[0x86]; got != "dagger" {
		t.Errorf("code 0x86 = %q, want %q: the implicit next code aliased onto it", got, "dagger")
	}
}

// An in-range /Differences overlay must still apply — the guard rejects
// impossible codes, not the feature.
func TestFontEncodingAppliesInRangeDifferences(t *testing.T) {
	r := &Reader{}
	enc := r.FontEncoding(Dict{"Encoding": Dict{
		"BaseEncoding": Name("WinAnsiEncoding"),
		"Differences":  Array{200, Name("alpha"), Name("beta")},
	}})
	if enc[200] != "alpha" || enc[201] != "beta" {
		t.Errorf("codes 200,201 = %q,%q; want alpha,beta", enc[200], enc[201])
	}
	// The overlay must not disturb the base encoding it sits on.
	if enc[0x85] != "ellipsis" {
		t.Errorf("code 0x85 = %q, want ellipsis", enc[0x85])
	}
}
