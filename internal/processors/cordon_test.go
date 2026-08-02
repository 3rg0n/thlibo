package processors

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCordonSignatureParity is the port's acceptance test: every
// signature and level must match what the Python produced, byte for byte.
// The fixtures live in cordon_parity_test.go and were captured from live
// Python before run.py was deleted, so they cannot be regenerated — see
// the comment there.
func TestCordonSignatureParity(t *testing.T) {
	for _, tc := range cordonParityCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cordonSignature(tc.line); got != tc.sig {
				t.Errorf("signature mismatch\n line: %s\n  got: %s\n want: %s", tc.line, got, tc.sig)
			}
			if got := cordonLevel(tc.line); got != tc.level {
				t.Errorf("level mismatch\n line: %s\n  got: %s\n want: %s", tc.line, got, tc.level)
			}
		})
	}
}

// The parity table is generated, so a silent truncation to zero rows
// would make the test above pass while checking nothing.
func TestCordonParityTableIsPopulated(t *testing.T) {
	if len(cordonParityCases) < 40 {
		t.Fatalf("parity table has %d cases, expected the full captured set", len(cordonParityCases))
	}
}

// The individually interesting cases from the table, called out by name so
// a regression in one names itself in the failure output instead of
// appearing as one row among 43.
func TestCordonSignatureEdgeCases(t *testing.T) {
	cases := []struct {
		name, line, sig, level string
	}{{
		// The raw level string survives into the signature while the level
		// field normalises it: sig keeps `err`, level reports `error`.
		name:  "level alias normalises in level but not sig",
		line:  `{"level":"ERR","msg":"boom"}`,
		sig:   "level=err;msg=boom",
		level: "error",
	}, {
		// An unrecognised structured level is "unknown" and does NOT fall
		// through to the regex, even though "error" appears in the line.
		name:  "unrecognised structured level does not fall through",
		line:  `{"level":"bogus","msg":"error happened"}`,
		sig:   "level=bogus;msg=error-happened",
		level: "unknown",
	}, {
		// Under three digits is not a status code, so it is not bucketed.
		name:  "two-digit status is not bucketed",
		line:  `{"status":42,"msg":"x"}`,
		sig:   "status=42;msg=x",
		level: "unknown",
	}, {
		// In the plain-text path the 8+-hex rule runs before the digits
		// rule, so a 10-digit Unix timestamp becomes <hex>, not <n> — while
		// the 3-digit status and the 1-digit version stay <n>.
		name:  "long digit run hits the hex rule first",
		line:  `10.0.0.1 - - [1778538117] "GET /api/v2/users/42 HTTP/1.1" 200`,
		sig:   "<ip>-<hex>-get-api-v<n>-users-<n>-http-<n>-<n>-<n>",
		level: "unknown",
	}, {
		name:  "blank line signature",
		line:  "   ",
		sig:   "unknown",
		level: "unknown",
	}, {
		// Five segments truncate to four.
		name:  "path stem keeps four segments",
		line:  `{"RequestPath":"/a/b/c/d/e"}`,
		sig:   "requestpath=/a/b/c/d",
		level: "unknown",
	}, {
		name:  "path segments tokenise",
		line:  `{"path":"/api/9f8e7d6c5b4a3210/users/42?x=1"}`,
		sig:   "path=/api/<hex>/users/<n>",
		level: "unknown",
	}, {
		// json.loads is whole-string, so trailing content after the object
		// makes the line plain text rather than structured.
		name:  "trailing garbage after JSON is plain text",
		line:  `{"level":"info"} trailing`,
		sig:   "level-info-trailing",
		level: "info",
	}, {
		// Valid JSON, but an array has no fields to lift.
		name:  "json array falls to plain text",
		line:  `["error","x"]`,
		sig:   "error-x",
		level: "error",
	}, {
		// A JSON object with no shape keys at all also falls through.
		name:  "object without shape keys falls to plain text",
		line:  `{"unrelated":"value"}`,
		sig:   "unrelated-value",
		level: "unknown",
	}, {
		name:  "leading log prefix before the object is skipped",
		line:  `ts="2026-08-02T00:00:00Z" {"level":"warn","msg":"disk almost full"}`,
		sig:   "level=warn;msg=disk-almost-full",
		level: "warn",
	}, {
		// Only the first three alphabetic words of a message are kept.
		name:  "msg keeps three words",
		line:  `{"msg":"Retry exhausted for upstream after 5 attempts"}`,
		sig:   "msg=retry-exhausted-for",
		level: "unknown",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cordonSignature(tc.line); got != tc.sig {
				t.Errorf("signature: got %q, want %q", got, tc.sig)
			}
			if got := cordonLevel(tc.line); got != tc.level {
				t.Errorf("level: got %q, want %q", got, tc.level)
			}
		})
	}
}

func TestCordonSignatureCaps(t *testing.T) {
	// Structured signatures cap at 200 characters.
	long := `{"msg":"` + strings.Repeat("alpha ", 3) + `","RequestPath":"/` +
		strings.Repeat("segment-with-a-very-long-name/", 3) + `","service":"` +
		strings.Repeat("s", 400) + `"}`
	if got := cordonSignature(long); len(got) > 200 {
		t.Errorf("structured signature is %d chars, cap is 200: %q", len(got), got)
	}
	// Plain-text signatures cap at 80.
	if got := cordonSignature(strings.Repeat("word ", 100)); len(got) > 80 {
		t.Errorf("plain signature is %d chars, cap is 80: %q", len(got), got)
	}
}

func TestCordonExtractKeys(t *testing.T) {
	line := `GET /api/v1/users 503 ERR42 v1.2.3 C:\Users\x\file.txt`
	got := cordonExtractKeys(line)
	want := []string{"/api/v1/users", "503", "ERR42", "v1.2.3", `C:\Users\x\file.txt`}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("keys: got %v, want %v", got, want)
	}
}

func TestCordonGroupKeysCapsAtTwelve(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("/path/unique-%d", i))
	}
	if got := cordonGroupKeys(lines); len(got) != 12 {
		t.Errorf("keys: got %d, want 12", len(got))
	}
}

func TestCordonGroupKeysDedupes(t *testing.T) {
	lines := []string{"/api/users 503", "/api/users 503", "/api/orders 503"}
	got := cordonGroupKeys(lines)
	want := []string{"/api/users", "503", "/api/orders"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("keys: got %v, want %v", got, want)
	}
}

func TestCordonFormatGroupsOrdering(t *testing.T) {
	// Two info lines, one error line. Error outranks info regardless of
	// count, so it must come first.
	lines := []string{
		`{"level":"info","msg":"served request"}`,
		`{"level":"info","msg":"served request"}`,
		`{"level":"error","msg":"upstream refused connection"}`,
	}
	out := cordonFormatGroups(lines, 100)
	if !strings.HasPrefix(out, "sig=level=error;") {
		t.Errorf("error group should sort first, got:\n%s", out)
	}
	if !strings.Contains(out, "count=2") {
		t.Errorf("info group should have count=2, got:\n%s", out)
	}
	if !strings.HasSuffix(out, "tail=100→2\n") {
		t.Errorf("tail line missing or wrong, got:\n%s", out)
	}
}

func TestCordonSample240Truncates(t *testing.T) {
	short := strings.Repeat("a", 240)
	if got := cordonSample240(short); got != short {
		t.Errorf("240 chars should pass through unchanged, got %d chars", len([]rune(got)))
	}
	long := strings.Repeat("a", 241)
	got := cordonSample240(long)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != 241 {
		t.Errorf("241 chars should be cut to 240 plus an ellipsis, got %d runes", len([]rune(got)))
	}
}

func TestCordonSample240DoesNotSplitRunes(t *testing.T) {
	// 300 multi-byte runes: a byte-based cut would produce invalid UTF-8.
	got := cordonSample240(strings.Repeat("é", 300))
	if strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("sample contains a replacement char, a rune was split: %q", got)
	}
	if len([]rune(got)) != 241 {
		t.Errorf("got %d runes, want 240 + ellipsis", len([]rune(got)))
	}
}

// --- windowing ---------------------------------------------------------

func TestCordonWindowsOverlap(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	cfg := cordonSettings{WindowSize: 10, WindowStride: 5, MaxChars: 0}
	got := cordonWindows(lines, cfg)
	// starts 0, 5, 10 — 15 would need lines[15:25].
	if len(got) != 3 {
		t.Fatalf("got %d windows, want 3", len(got))
	}
	for i, want := range []int{0, 5, 10} {
		if got[i].start != want {
			t.Errorf("window %d starts at %d, want %d", i, got[i].start, want)
		}
	}
	if n := len(strings.Split(got[0].text, "\n")); n != 10 {
		t.Errorf("window holds %d lines, want 10", n)
	}
}

func TestCordonWindowsRespectMaxChars(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = strings.Repeat("x", 100)
	}
	cfg := cordonSettings{WindowSize: 10, WindowStride: 5, MaxChars: 50}
	got := cordonWindows(lines, cfg)
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	if len(got[0].text) != 50 {
		t.Errorf("window text is %d chars, want the 50-char cap", len(got[0].text))
	}
}

func TestCordonWindowsTooFewLines(t *testing.T) {
	cfg := cordonSettings{WindowSize: 10, WindowStride: 5}
	if got := cordonWindows(make([]string, 9), cfg); got != nil {
		t.Errorf("9 lines cannot fill a 10-line window, got %d windows", len(got))
	}
}

func TestCordonSampleStridesNotTruncates(t *testing.T) {
	windows := make([]cordonWindow, 100)
	for i := range windows {
		windows[i].start = i
	}
	got := cordonSample(windows, 10)
	if len(got) != 10 {
		t.Fatalf("got %d windows, want 10", len(got))
	}
	// Striding must reach the tail; truncating would stop at start=9.
	if got[9].start != 90 {
		t.Errorf("last sampled window starts at %d, want 90 — sampling truncated instead of striding", got[9].start)
	}
}

func TestCordonSamplePassesThroughUnderCap(t *testing.T) {
	windows := make([]cordonWindow, 5)
	if got := cordonSample(windows, 10); len(got) != 5 {
		t.Errorf("got %d, want the input unchanged", len(got))
	}
	if got := cordonSample(windows, 0); len(got) != 5 {
		t.Errorf("max=0 means no cap, got %d", len(got))
	}
}

func TestCordonNonBlankLinesDropsBlanksAndCR(t *testing.T) {
	got := cordonNonBlankLines("a\r\n\r\n  \nb\n")
	want := []string{"a", "b"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// --- k-NN scoring ------------------------------------------------------

func TestCordonKNNScoresRanksTheOutlier(t *testing.T) {
	// Four vectors clustered at the origin, one far away. The distant one
	// must score highest.
	vectors := [][]float64{
		{0, 0}, {0, 0.1}, {0.1, 0}, {0.1, 0.1},
		{50, 50},
	}
	scores := cordonKNNScores(vectors, 2)
	outlier := 4
	for i := range vectors {
		if i != outlier && scores[i] >= scores[outlier] {
			t.Errorf("vector %d scored %v, not below the outlier's %v", i, scores[i], scores[outlier])
		}
	}
}

func TestCordonKNNScoresKnownValue(t *testing.T) {
	// Three points on a line at 0, 3, 4. For k=1: nearest neighbour
	// distances are 3, 1, 1.
	scores := cordonKNNScores([][]float64{{0}, {3}, {4}}, 1)
	want := []float64{3, 1, 1}
	for i := range want {
		if scores[i] != want[i] {
			t.Errorf("score %d = %v, want %v", i, scores[i], want[i])
		}
	}
	// For k=2: means of (3,4), (1,3), (1,4) -> 3.5, 2, 2.5.
	scores = cordonKNNScores([][]float64{{0}, {3}, {4}}, 2)
	want = []float64{3.5, 2, 2.5}
	for i := range want {
		if scores[i] != want[i] {
			t.Errorf("k=2 score %d = %v, want %v", i, scores[i], want[i])
		}
	}
}

func TestCordonKNNScoresClampsK(t *testing.T) {
	// k larger than the neighbour count clamps rather than panicking.
	scores := cordonKNNScores([][]float64{{0}, {1}}, 5)
	if len(scores) != 2 || scores[0] != 1 || scores[1] != 1 {
		t.Errorf("got %v, want [1 1]", scores)
	}
}

func TestCordonKNNScoresSingleVector(t *testing.T) {
	if scores := cordonKNNScores([][]float64{{0, 0}}, 5); len(scores) != 1 || scores[0] != 0 {
		t.Errorf("got %v, want [0]", scores)
	}
}

func TestCordonSquaredDistanceTolerantOfLengthMismatch(t *testing.T) {
	// Must not panic; compares the common prefix.
	if got := cordonSquaredDistance([]float64{1, 2, 3}, []float64{0}); got != 1 {
		t.Errorf("got %v, want 1", got)
	}
	if got := cordonSquaredDistance([]float64{0}, []float64{1, 2, 3}); got != 1 {
		t.Errorf("got %v, want 1", got)
	}
}

// --- config ------------------------------------------------------------

func TestCordonEnvDuration(t *testing.T) {
	cases := []struct {
		set  string
		want time.Duration
	}{
		{"", 7 * time.Second},            // unset -> default
		{"1.5", 1500 * time.Millisecond}, // Python-style float seconds
		{"2s", 2 * time.Second},          // Go duration string
		{"0", 0},                         // explicit no-timeout
		{"garbage", 7 * time.Second},     // unparseable -> default
	}
	for _, tc := range cases {
		t.Setenv("CORDON_TEST_DUR", tc.set)
		if got := envDuration("CORDON_TEST_DUR", 7*time.Second); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.set, got, tc.want)
		}
	}
}

func TestCordonEnvIntFallsBackOnGarbage(t *testing.T) {
	t.Setenv("CORDON_TEST_INT", "not-a-number")
	if got := envInt("CORDON_TEST_INT", 42); got != 42 {
		t.Errorf("got %d, want the default 42", got)
	}
	t.Setenv("CORDON_TEST_INT", "7")
	if got := envInt("CORDON_TEST_INT", 42); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

// --- end-to-end through the filter ------------------------------------

// stubEmbedder scores each window by a caller-supplied function of its
// text, so the ranking is fully determined. A real model's vectors are
// the whole input to the ranking, so without this the filter's output is
// unpinnable.
type stubEmbedder struct {
	vec    func(text string) []float64
	err    error
	calls  int
	inputs []string
}

func (s *stubEmbedder) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	s.calls++
	s.inputs = append(s.inputs, inputs...)
	if s.err != nil {
		return nil, s.err
	}
	out := make([][]float64, len(inputs))
	for i, in := range inputs {
		out[i] = s.vec(in)
	}
	return out, nil
}

// withEmbedder swaps the package-level embedder factory for one test.
func withEmbedder(t *testing.T, e cordonEmbedder) {
	t.Helper()
	prev := cordonEmbedderFor
	cordonEmbedderFor = func(time.Duration) cordonEmbedder { return e }
	t.Cleanup(func() { cordonEmbedderFor = prev })
}

// cordonTestInput builds n boring lines with one anomaly at the given
// index, and returns the raw text.
func cordonTestInput(n, anomalyAt int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf(`{"level":"info","msg":"served request","path":"/api/users","seq":%d}`, i)
	}
	if anomalyAt >= 0 && anomalyAt < n {
		lines[anomalyAt] = `{"level":"error","msg":"upstream refused connection","path":"/api/payments"}`
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestCordonFilterSurfacesTheAnomaly(t *testing.T) {
	const n, anomalyAt = 60, 32
	// A window is "far" if it contains the anomaly. One dimension is
	// enough to make the ranking unambiguous.
	withEmbedder(t, &stubEmbedder{vec: func(text string) []float64 {
		if strings.Contains(text, "upstream refused") {
			return []float64{100}
		}
		return []float64{0}
	}})

	out := cordonFilter(context.Background(), []byte(cordonTestInput(n, anomalyAt)))
	got := string(out)

	if !strings.Contains(got, "level=error") {
		t.Errorf("anomalous line was not surfaced:\n%s", got)
	}
	if !strings.Contains(got, "sample=") || !strings.Contains(got, "keys=") {
		t.Errorf("output is not in the grouped shape:\n%s", got)
	}
	if !strings.Contains(got, fmt.Sprintf("tail=%d→", n)) {
		t.Errorf("tail line should record %d input lines:\n%s", n, got)
	}
	if len(out) >= len(cordonTestInput(n, anomalyAt)) {
		t.Errorf("output (%d bytes) is not smaller than input", len(out))
	}
	// The error group must sort ahead of the info group.
	if !strings.HasPrefix(got, "sig=level=error;") {
		t.Errorf("error group should be first:\n%s", got)
	}
}

func TestCordonFilterPassesThroughOnEmbedError(t *testing.T) {
	in := []byte(cordonTestInput(60, 32))
	withEmbedder(t, &stubEmbedder{err: errors.New("dial: no such file")})
	if got := cordonFilter(context.Background(), in); string(got) != string(in) {
		t.Errorf("embed failure must pass the input through verbatim")
	}
}

func TestCordonFilterPassesThroughOnBackendNotReady(t *testing.T) {
	// The fail-open case the middleware relies on (ADR 0006).
	in := []byte(cordonTestInput(60, 32))
	withEmbedder(t, &stubEmbedder{err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded)})
	if got := cordonFilter(context.Background(), in); string(got) != string(in) {
		t.Errorf("timeout must pass the input through verbatim")
	}
}

func TestCordonFilterPassesThroughOnShortInput(t *testing.T) {
	// Below 2*WindowSize lines the embedder must not even be called.
	stub := &stubEmbedder{vec: func(string) []float64 { return []float64{0} }}
	withEmbedder(t, stub)
	in := []byte(cordonTestInput(19, 5)) // 19 < 2*10
	if got := cordonFilter(context.Background(), in); string(got) != string(in) {
		t.Errorf("short input must pass through verbatim")
	}
	if stub.calls != 0 {
		t.Errorf("embedder was called %d times on short input, want 0", stub.calls)
	}
}

func TestCordonFilterPassesThroughOnSingleWindow(t *testing.T) {
	// 20 lines clears the 2*WindowSize gate but with stride 5 yields
	// windows at 0,5,10 — so force a single window via a wide stride.
	t.Setenv("CORDON_WINDOW_STRIDE", "100")
	stub := &stubEmbedder{vec: func(string) []float64 { return []float64{0} }}
	withEmbedder(t, stub)
	in := []byte(cordonTestInput(25, 5))
	if got := cordonFilter(context.Background(), in); string(got) != string(in) {
		t.Errorf("a single window must pass through verbatim")
	}
	if stub.calls != 0 {
		t.Errorf("embedder was called %d times, want 0", stub.calls)
	}
}

func TestCordonFilterBatches(t *testing.T) {
	t.Setenv("CORDON_BATCH_SIZE", "4")
	stub := &stubEmbedder{vec: func(string) []float64 { return []float64{0} }}
	withEmbedder(t, stub)
	// 200 lines, stride 5, window 10 -> 39 windows -> 10 batches of <= 4.
	cordonFilter(context.Background(), []byte(cordonTestInput(200, 100)))
	if stub.calls != 10 {
		t.Errorf("got %d batches, want 10", stub.calls)
	}
	if len(stub.inputs) != 39 {
		t.Errorf("got %d embedded windows, want 39", len(stub.inputs))
	}
}

func TestCordonFilterRejectsShortVectorList(t *testing.T) {
	// A daemon that returns fewer vectors than inputs would make cordon
	// score the wrong text; the filter must pass through instead.
	in := []byte(cordonTestInput(60, 32))
	withEmbedder(t, &truncatingEmbedder{})
	if got := cordonFilter(context.Background(), in); string(got) != string(in) {
		t.Errorf("a short vector list must pass the input through verbatim")
	}
}

type truncatingEmbedder struct{}

func (truncatingEmbedder) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float64, len(inputs)-1)
	for i := range out {
		out[i] = []float64{0}
	}
	return out, nil
}

func TestCordonFilterHonoursCancelledContext(t *testing.T) {
	// The reason cordon needs NativeCtxFilter at all: a cancelled caller
	// must not get a round-trip. The stub reports the context error the
	// way a real dial would.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := []byte(cordonTestInput(60, 32))
	withEmbedder(t, &ctxCheckingEmbedder{})
	if got := cordonFilter(ctx, in); string(got) != string(in) {
		t.Errorf("cancelled context must pass the input through verbatim")
	}
}

type ctxCheckingEmbedder struct{}

func (ctxCheckingEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float64, len(inputs))
	for i := range out {
		out[i] = []float64{0}
	}
	return out, nil
}

func TestCordonFilterEmittedLinesStayInInputOrder(t *testing.T) {
	// Selection is by score, but the emitted lines must follow the log's
	// own order — a reordered log is harder to read than a longer one.
	// Two anomalies, the later one scoring higher.
	//
	// 60 lines at window 10 / stride 5 gives 11 windows, of which 4 contain
	// an anomaly (two per anomaly, thanks to the overlap). The default 20%
	// would select only 2 and so only ever surface the higher-scoring one,
	// which is correct behaviour but tests nothing about ordering.
	t.Setenv("CORDON_TOP_PERCENTILE", "40")
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf(`{"level":"info","msg":"served request","seq":%d}`, i)
	}
	lines[10] = `{"level":"warn","msg":"first anomaly"}`
	lines[50] = `{"level":"warn","msg":"second anomaly"}`
	in := []byte(strings.Join(lines, "\n") + "\n")

	withEmbedder(t, &stubEmbedder{vec: func(text string) []float64 {
		switch {
		case strings.Contains(text, "second anomaly"):
			return []float64{200}
		case strings.Contains(text, "first anomaly"):
			return []float64{100}
		}
		return []float64{0}
	}})

	got := string(cordonFilter(context.Background(), in))
	first := strings.Index(got, "first-anomaly")
	second := strings.Index(got, "second-anomaly")
	if first < 0 || second < 0 {
		t.Fatalf("both anomalies should surface, got:\n%s", got)
	}
	// Both are warn with count 1, so the signature tiebreak decides:
	// "msg=first-anomaly" < "msg=second-anomaly".
	if first > second {
		t.Errorf("groups are not in the documented tiebreak order:\n%s", got)
	}
}

func TestCordonFilterRegistered(t *testing.T) {
	if nativeFilter("cordon-filter") == nil {
		t.Fatal("cordon-filter is not registered as a native filter")
	}
}

// A wedged daemon must not hang the hook. The dispatcher's 30s
// ScriptTimeout used to provide this bound; a native filter has to carry
// it itself.
func TestCordonFilterBoundsTotalTime(t *testing.T) {
	t.Setenv("CORDON_TIMEOUT", "0.05")
	t.Setenv("CORDON_BATCH_SIZE", "1")
	in := []byte(cordonTestInput(200, 100))
	withEmbedder(t, &slowEmbedder{delay: 20 * time.Millisecond})

	start := time.Now()
	got := cordonFilter(context.Background(), in)
	elapsed := time.Since(start)

	if string(got) != string(in) {
		t.Errorf("a timeout must pass the input through verbatim")
	}
	// 39 windows x 20ms = 780ms if unbounded; the 50ms budget must cut it
	// far short. The generous ceiling keeps this from flaking on a loaded
	// CI box while still failing if the bound is absent.
	if elapsed > 400*time.Millisecond {
		t.Errorf("filter ran %v, past its 50ms budget — the total bound is not applied", elapsed)
	}
}

type slowEmbedder struct{ delay time.Duration }

func (s slowEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	out := make([][]float64, len(inputs))
	for i := range out {
		out[i] = []float64{0}
	}
	return out, nil
}
