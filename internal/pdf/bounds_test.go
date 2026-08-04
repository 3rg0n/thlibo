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

// rawPDF assembles objects into a file with a correct xref table, so a test
// can vary one file-controlled field and leave everything else valid.
func rawPDF(objs []string, trailerExtra string) []byte {
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
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R %s>>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, trailerExtra, xref)
	return []byte(b.String())
}

// TestStreamLengthIsNotTrusted is the regression test for the panic
// FuzzOpenBytes found on seed 348258312e3b1b8f: `/Length` is a number the
// file asserts about its own stream, and upstream sliced the buffer on it
// directly — "slice bounds out of range [:964] with capacity 768".
//
// This one is a panic rather than a hang, so RunNative's recover does keep
// the fail-open contract intact. It is still worth fixing at the source: a
// recovered panic costs the user all compression on that document, and the
// correct read of an overlong /Length is "the length is wrong, go find
// endstream" — which recovers real text instead of discarding the file.
func TestStreamLengthIsNotTrusted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length string
	}{
		{"far past EOF", "999999"},
		{"just past EOF", "4000"},
		{"negative", "-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := rawPDF([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
				"<< /Length " + tc.length + " >>\nstream\nBT /F1 12 Tf (hello) Tj ET\nendstream",
			}, "")

			// Must not panic: the read path is reached via TextLines.
			mustTerminate(t, "stream read with a bad /Length", func() {
				readEverything(data)
			})
		})
	}
}

// TestObjStmEntryCountIsNotTrusted covers the third file-declared size that
// reaches an allocation: /N on a compressed object stream. Upstream did
// `make([]objEntry, n)` straight from the dict, so /N 1000000000 asks for
// ~16 GB before a single entry is validated.
//
// This drives resolveFromObjStm directly rather than through a whole file.
// Reaching it via OpenBytes needs a compressed xref entry pointing at the
// stream, and getting that wrong yields a test that passes because it never
// arrives — which is exactly what the first draft of this test did.
func TestObjStmEntryCountIsNotTrusted(t *testing.T) {
	for _, n := range []int{1000000000, -1, 1 << 40} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			r := &Reader{
				xref:       make(map[int]int64),
				compressed: make(map[int]compressedRef),
				cache: map[int]any{
					// A stream far too small to hold the entries it declares.
					9: &Stream{
						Dict: Dict{"Type": Name("ObjStm"), "N": n, "First": 4},
						Data: []byte("1 0 2 8 "),
					},
				},
			}

			var got any
			mustTerminate(t, "object stream with a bogus /N", func() {
				got = r.resolveFromObjStm(compressedRef{StreamObj: 9, Index: 0})
			})
			// A stream that cannot hold its declared entries resolves to
			// nothing. The bug was the allocation on the way to that answer.
			if got != nil {
				t.Errorf("resolved %#v from an ObjStm declaring /N %d, want nil", got, n)
			}
		})
	}
}

// TestXRefOffsetIsNotTrusted is the regression test for the panic
// FuzzOpenBytes found on seed 01baf1fea1afef92: an xref subsection entry of
// "-1 0 n" declares object 4 lives at offset -1, and that number went
// straight into Lexer.SetPos, so skipWhitespaceAndComments indexed data[-1]
// — "index out of range [-1]".
//
// The compressed-object path (resolveFromObjStm) already range-checked its
// own offset; the uncompressed path did not, which is the whole bug. Both
// negative and past-EOF are covered because they fail differently: negative
// panics, past-EOF reads as EOF and merely returns nothing.
//
// This asserts on the returned error rather than going through
// mustTerminate: that helper runs its closure in a goroutine, where a panic
// is unrecoverable and takes the test binary down instead of failing a case.
func TestXRefOffsetIsNotTrusted(t *testing.T) {
	r := &Reader{
		data:  []byte("%PDF-1.7\n1 0 obj\n42\nendobj\n"),
		xref:  map[int]int64{},
		cache: map[int]any{},
	}
	for _, tc := range []struct {
		name string
		pos  int
	}{
		{"negative", -1},
		{"far negative", -1 << 40},
		{"one past the end", len(r.data)},
		{"far past the end", 1 << 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj, err := r.parseObjectAt(tc.pos)
			if err == nil {
				t.Errorf("offset %d resolved to %#v, want an error", tc.pos, obj)
			}
		})
	}

	// And the same thing end to end, since the guard only matters if the
	// offset actually reaches it through Resolve.
	r.xref[4] = -1
	if got := r.Resolve(Ref{Num: 4}); got != nil {
		t.Errorf("resolved %#v from a negative xref offset, want nil", got)
	}
}

// TestXRefChainOffsetIsNotTrusted covers the *other* file-declared position
// that reaches the lexer, which the parseObjectAt guard above does not touch:
// readXRefAt takes its offset from `startxref` and from each /Prev in the
// update chain, and neither is range-checked at the call site.
//
// /Prev is an ordinary PDF integer, so "-1" is a perfectly parseable value
// for it — and it panicked the lexer on data[-1] exactly like the xref entry
// did. The clamp in Lexer.SetPos is what stops both, and this is the case
// that makes that clamp load-bearing rather than defensive: reverting it with
// only the parseObjectAt guard in place still crashes here.
func TestXRefChainOffsetIsNotTrusted(t *testing.T) {
	for _, prev := range []string{"-1", "-99999", "999999"} {
		t.Run(prev, func(t *testing.T) {
			data := rawPDF([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
				"<< /Length 30 >>\nstream\nBT /F1 12 Tf (hi) Tj ET\nendstream",
			}, "/Prev "+prev+" ")

			// Reading must not panic. Whether the document opens at all is
			// not the contract — an out-of-range /Prev makes the chain
			// unreadable, and returning no text is a fine answer.
			readEverything(data)
		})
	}
}

// TestPredictorParametersAreBounded covers the allocation reachable through
// /DecodeParms. bytesPerRow is Columns*Colors*BitsPerComponent/8, all three
// file-controlled, and the product sizes the output buffer in pngUnpredict.
// Checking the product alone is not enough: at these values it overflows
// int64 and comes back small and innocent-looking, which is why each factor
// is range-checked separately.
//
// The assertion is on the *reason*, not merely on "an error came back".
// pngUnpredict rejects most of these incidentally, for not dividing evenly
// into the row size — so a test that accepted any error passed with the
// bounds removed, and asserted nothing.
func TestPredictorParametersAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name string
		parm Dict
	}{
		{"huge columns", Dict{"Predictor": 12, "Columns": 1000000000}},
		{"overflowing product", Dict{"Predictor": 12, "Columns": 1 << 40, "Colors": 1 << 40, "BitsPerComponent": 8}},
		{"negative columns", Dict{"Predictor": 12, "Columns": -1}},
		{"huge colors", Dict{"Predictor": 12, "Columns": 8, "Colors": 1 << 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			mustTerminate(t, "predictor with out-of-range parameters", func() {
				_, err = applyPredictorWithParms([]byte("\x00\x01\x02\x03"), tc.parm)
			})
			if err == nil {
				t.Fatal("accepted out-of-range predictor parameters, want an error")
			}
			if !strings.Contains(err.Error(), "out of range") {
				t.Errorf("rejected for the wrong reason: %v\n"+
					"want the range check to fire, not an incidental row-size mismatch", err)
			}
		})
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

// TestObjectNestingIsBounded is the regression test for the one defect in
// this file that no fuzz target found and none would: unbounded array and
// dictionary nesting.
//
// parseArray and parseDict recurse into each other through parseFromToken,
// so `[[[[…` descends once per byte of input. Go grows a goroutine stack on
// demand to 1 GB and then calls runtime.throw — which `recover()` cannot
// catch. So this is the *worst* class in this file's taxonomy: not a hang
// that blocks the hook and not a panic that RunNative absorbs, but a fatal
// exit that takes the process down with the tool output still unwritten.
// Measured before the fix, end to end through `thlibo compress`: exit 2 and
// empty stdout at ~1.3M levels — a 2.6 MB file, against the 64 MiB the
// middleware accepts.
//
// The fuzzers cannot reach it. Their contract is "no panic, no hang, no
// unbounded allocation", and a fatal stack overflow satisfies none of those
// checks because it never returns to the harness at all. More basically,
// fuzzing does not generate multi-megabyte inputs of one repeated byte.
//
// Asserted directly on the Parser rather than through mustTerminate: that
// helper runs its closure in a goroutine, and neither a fatal throw nor a
// panic there can fail a case — it kills the test binary.
func TestObjectNestingIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		open  string
		close string
	}{
		{"arrays", "[", "]"},
		{"dicts", "<< /K ", ">>"},
		// Alternating is the case that a guard on only one of the two
		// functions would miss: neither counter alone reaches its bound.
		{"alternating", "[ << /K ", ">> ]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Well past maxObjectDepth but small enough to parse instantly;
			// the point is that the bound fires, not how deep it can go.
			const depth = maxObjectDepth * 4
			body := strings.Repeat(tc.open, depth) + "null" + strings.Repeat(tc.close, depth)

			p := NewParser([]byte(body))
			obj, err := p.ParseObject()
			if err == nil {
				t.Fatalf("parsed %d levels of nesting without error, got %T", depth, obj)
			}
			if !strings.Contains(err.Error(), "nesting exceeds") {
				t.Errorf("error = %v, want it to name the nesting bound", err)
			}
		})
	}

	// The bound must not reject the nesting real producers emit. Four levels
	// is already deeper than anything in a normal document.
	p := NewParser([]byte("[[[[1 2 3]]]]"))
	if _, err := p.ParseObject(); err != nil {
		t.Errorf("rejected four levels of legitimate nesting: %v", err)
	}
	// And depth must unwind: a document with many sibling deep-ish objects
	// must not accumulate toward the bound. This fails if the guard
	// increments without a matching decrement.
	deep := strings.Repeat("[", 600) + "1" + strings.Repeat("]", 600)
	p = NewParser([]byte(deep + " " + deep))
	if _, err := p.ParseObject(); err != nil {
		t.Fatalf("first 600-level object: %v", err)
	}
	if _, err := p.ParseObject(); err != nil {
		t.Errorf("second 600-level object failed, so depth did not unwind: %v", err)
	}
}
