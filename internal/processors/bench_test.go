package processors

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/3rg0n/thlibo/internal/inferd"
)

// Benchmarks for the two native filters whose cost is load-bearing rather
// than incidental.
//
// Why these two and not the other eleven. A native filter runs inside a
// PreToolUse hook, so its wall-clock is added to every tool call the user
// makes — but for a line-oriented filter (git, npm, pytest, lint) the work
// is a single pass over the input and the cost is self-evidently linear.
// These two are different:
//
//   - pdf-filter is the headline performance claim. README and ADR 0015
//     publish "~99× faster than the Python path", measured once by hand
//     during the port. Nothing defended it afterwards, so a change that
//     gave back an order of magnitude would ship silently and the docs
//     would keep asserting the old number.
//   - cordon-filter's k-NN pass is O(n²) in the window count and is the
//     one place in the tree where a *performance* regression is an
//     *availability* regression. See TestCordonKNNScoringStaysUnderBudget.
//
// These are regression detectors, not the ADR's measurement. The ADR
// compared Go against Python on real multi-megabyte documents; these run
// on the synthetic fixtures, in-process, with no Python anywhere. So the
// absolute numbers here are not the published 99× and must not be quoted
// as it — what they catch is *this* path getting slower than it was, which
// is the thing that actually goes unnoticed.
//
// Run:
//
//	go test ./internal/processors/ -run '^$' -bench . -benchmem
//	go test ./internal/processors/ -run '^$' -bench PDFFilter -count 10
//
// Compare two revisions with golang.org/x/perf/cmd/benchstat rather than
// eyeballing one run — the variance between runs on a laptop is wider than
// most regressions worth catching.

// BenchmarkPDFFilter measures the whole filter, not just extraction: parse,
// tier selection, text grouping, table geometry, and markdown rendering.
// That is the surface the hook actually pays for, and picking a narrower
// entry point would hide a regression in any of the other stages.
//
// Scaled by page count because the tier-3 table detector is the part most
// likely to acquire an accidental quadratic, and it works per page over
// that page's spans — a per-page cost that stays flat as pages grow is the
// signal worth watching.
func BenchmarkPDFFilter(b *testing.B) {
	for _, pages := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("pages=%d", pages), func(b *testing.B) {
			content := make([]string, pages)
			for i := range content {
				content[i] = pdfTextStream(benchPageLines(i)...)
			}
			input := buildTestPDF(content, "")

			// SetBytes gives MB/s, which is the number that stays
			// comparable across page counts — ns/op does not, since the
			// document itself grows.
			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out := pdfFilter(input)
				// Guard against the compiler eliding the call, and against
				// a fixture that silently starts hitting the fail-open path
				// — a benchmark of the passthrough branch measures nothing.
				if len(out) == 0 {
					b.Fatal("empty output")
				}
			}
		})
	}
}

// benchPageLines builds one page's worth of prose with a little structure,
// so the tier ladder has something to do beyond returning a single line.
// Deliberately not uniform: identical lines would let the span grouper
// exit early in a way a real document never does.
func benchPageLines(page int) []string {
	lines := make([]string, 0, 24)
	lines = append(lines, fmt.Sprintf("Section %d. Operational Considerations", page+1))
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf(
			"Paragraph %d.%d discusses the handling of tool output exceeding %d bytes.",
			page+1, i+1, (i+1)*512))
	}
	lines = append(lines, "Name          Status      Count")
	lines = append(lines, "alpha         ready       42")
	lines = append(lines, "beta          pending     7")
	return lines
}

// BenchmarkCordonKNNScores measures the pairwise-distance pass in
// isolation, at the real embedding width (inferd.EmbedDimensions), with no
// embedder involved. Isolating it is the point: the filter's other cost is
// a network round-trip whose duration says more about the daemon than
// about this code, and averaging the two together would mask exactly the
// growth this benchmark exists to track.
//
// The window counts bracket the numbers quoted in cordonKNNScores' own
// comment (1k → 0.17s, 5k → 5.8s, 10k → 28s). If those drift, that comment
// and this benchmark should be updated together — a stale measurement in a
// comment justifying a timeout is worse than no measurement.
func BenchmarkCordonKNNScores(b *testing.B) {
	for _, n := range []int{256, 1024, 4096} {
		b.Run(fmt.Sprintf("windows=%d", n), func(b *testing.B) {
			vectors := benchVectors(n, inferd.EmbedDimensions)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := cordonKNNScores(context.Background(), vectors, 5); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCordonFilter measures the filter end to end with an in-process
// embedder, so the result is windowing + embedding-call overhead + scoring
// + grouping with the network removed. The gap between this and
// BenchmarkCordonKNNScores at a comparable window count is what the
// non-scoring stages cost.
func BenchmarkCordonFilter(b *testing.B) {
	for _, lines := range []int{200, 2000} {
		b.Run(fmt.Sprintf("lines=%d", lines), func(b *testing.B) {
			input := []byte(cordonTestInput(lines, lines/2))

			// A deterministic embedder that is cheap but not constant: a
			// constant vector would collapse every distance to zero and let
			// the sort run on pre-sorted input, which is not the case being
			// measured.
			prev := cordonEmbedderFor
			cordonEmbedderFor = func(time.Duration) cordonEmbedder {
				return &benchEmbedder{dims: inferd.EmbedDimensions}
			}
			b.Cleanup(func() { cordonEmbedderFor = prev })

			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if out := cordonFilter(context.Background(), input); len(out) == 0 {
					b.Fatal("empty output")
				}
			}
		})
	}
}

// benchEmbedder returns a vector derived from the text, cheaply. Not
// stubEmbedder: that one records every input it sees, which turns a
// benchmark loop into an unbounded append and measures the recorder.
type benchEmbedder struct{ dims int }

func (e *benchEmbedder) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	out := make([][]float64, len(inputs))
	for i, in := range inputs {
		v := make([]float64, e.dims)
		h := len(in)
		for d := range v {
			h = h*31 + int(in[(d+i)%len(in)])
			v[d] = float64(h%1000) / 1000.0
		}
		out[i] = v
	}
	return out, nil
}

// benchVectors builds n distinct unit-ish vectors of the given width.
// Spread deterministically rather than randomly so a run is comparable to
// the one before it — math/rand without a fixed seed would put noise
// straight into the thing being measured.
func benchVectors(n, dims int) [][]float64 {
	vectors := make([][]float64, n)
	for i := range vectors {
		v := make([]float64, dims)
		for d := range v {
			v[d] = float64((i*7+d*13)%1000) / 1000.0
		}
		vectors[i] = v
	}
	return vectors
}

// TestCordonKNNScoringStaysUnderBudget is the assertion that makes the
// cordon benchmark more than a number in a log, and it encodes the bug
// fixed in #106 as a standing test.
//
// The failure it guards is not "cordon got slower". It is: the O(n²)
// scoring pass grows until it can no longer finish inside CORDON_TIMEOUT
// (30s) on an input cordon is actually reached with, at which point the
// deadline fires, the filter returns the input verbatim, and cordon
// silently becomes a no-op. Every existing test still passes — passthrough
// IS the documented behaviour on a deadline (invariant #2) — so nothing
// else in the suite can tell the difference between "correctly failed
// open" and "never works any more".
//
// Deliberately loose. The ceiling is ~7% of CORDON_TIMEOUT for a window
// count that leaves headroom for the embedding round-trip the real filter
// also has to pay for out of the same 30s. A shared CI runner under load
// is several times slower than a developer laptop, and a flaky perf test
// gets deleted rather than investigated — so this is sized to catch a
// change in *complexity* (an accidental cubic, a ctx check moved into the
// inner loop, a per-distance allocation), not a change in constant factor.
// benchstat over BenchmarkCordonKNNScores is the tool for the latter.
func TestCordonKNNScoringStaysUnderBudget(t *testing.T) {
	const (
		windows = 2048
		budget  = 2 * time.Second
	)

	vectors := benchVectors(windows, inferd.EmbedDimensions)

	start := time.Now()
	scores, err := cordonKNNScores(context.Background(), vectors, 5)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("scoring %d windows failed: %v", windows, err)
	}
	if len(scores) != windows {
		t.Fatalf("got %d scores for %d windows", len(scores), windows)
	}
	if elapsed > budget {
		t.Errorf("k-NN scoring of %d windows took %v, over the %v budget.\n"+
			"This is an availability problem, not a speed one: at this rate the "+
			"pass cannot finish inside CORDON_TIMEOUT (%v) on the inputs cordon "+
			"is reached with, so the deadline fires and the filter silently "+
			"degrades to permanent passthrough. Check for an added nested loop "+
			"or a per-distance allocation.",
			windows, elapsed, budget, cordonConfig().TotalTimeout)
	}
	t.Logf("%d windows × %d dims scored in %v", windows, inferd.EmbedDimensions, elapsed)
}

// TestPDFFilterStaysUnderBudget is the pdf-filter counterpart, and the same
// distinction applies: the risk is not an unflattering number, it is the
// filter crossing back over the threshold where the Python path's cost was
// the reason to replace it. ADR 0015's argument was concrete — "a
// 38-second hook is a hook the user experiences as a hang; a 302 ms one is
// invisible" — so a regression that puts a hook back into seconds
// invalidates the decision, not just the benchmark.
//
// The budget is scaled to the fixture, not lifted from the ADR. ADR 0015's
// numbers are for real multi-megabyte documents; this one is 167 KB, which
// at the rates measured there is single-digit milliseconds. Measured at
// ~10 ms, so 1s is roughly a 100× margin — loose enough to survive a
// contended CI runner, tight enough that it does not sit above the
// "multi-second hook reads as a hang" line the ADR argued from. It will
// catch a complexity change or a large constant regression; a 2× one is
// benchstat's job, not this test's.
func TestPDFFilterStaysUnderBudget(t *testing.T) {
	const (
		pages  = 64
		budget = time.Second
	)

	content := make([]string, pages)
	for i := range content {
		content[i] = pdfTextStream(benchPageLines(i)...)
	}
	input := buildTestPDF(content, "")

	start := time.Now()
	out := pdfFilter(input)
	elapsed := time.Since(start)

	// A fail-open passthrough would be fast and would pass a naive timing
	// check, so assert the filter actually did the work first.
	if len(out) == 0 {
		t.Fatal("empty output")
	}
	if strings.HasPrefix(string(out), "%PDF-") {
		t.Fatalf("filter fell open and returned the source document; "+
			"the timing below measures nothing (%v)", elapsed)
	}
	if elapsed > budget {
		t.Errorf("pdfFilter on a %d-page, %d-byte document took %v, over the %v budget.\n"+
			"ADR 0015 replaced the Python path because a multi-second hook reads "+
			"as a hang; a regression here gives that back. Check for a quadratic "+
			"in the span grouper or the table detector.",
			pages, len(input), elapsed, budget)
	}
	t.Logf("%d pages (%d bytes in, %d bytes out) filtered in %v",
		pages, len(input), len(out), elapsed)
}
