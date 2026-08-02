package processors

// cordon-filter: surface semantically rare windows in a log stream.
//
// Native Go port of processors/cordon-filter/run.py (ADR 0010), the last
// Python script processor and the only one that needed a network
// round-trip — which is why it went last and why it needed
// RegisterNativeCtx (see native.go).
//
// Overlapping windows of WINDOW_SIZE records are embedded via inferd's
// embed socket, then scored by k-NN distance in embedding space (mean
// distance to the K nearest neighbours). Higher score = more isolated =
// more anomalous. The top-percentile windows are expanded back to their
// source lines and emitted in the same grouped shape `compress` produces,
// so downstream consumers don't need a second parser.
//
// Reference: github.com/calebevans/cordon (Apache-2.0) is the algorithmic
// blueprint; this is a clean implementation against inferd's embedding
// endpoint, with no torch and no sentence-transformers.
//
// Fallback contract (invariant #2 — the filter never breaks the AI
// client). Every one of these returns the input verbatim:
//
//   - fewer than 2*WINDOW_SIZE non-blank lines
//   - fewer than 2 windows
//   - embed socket unreachable, erroring, or slow past the budget
//   - any panic (RunNativeCtx recovers it)
//
// Two behaviours differ from the retired Python, both deliberate:
//
//   - The Python had no numpy-missing branch to port; the k-NN step is
//     ~40 lines of Go and has no dependency to be missing.
//   - The Python returned its grouped output unconditionally. Here
//     RunNativeCtx's monotonic guard applies, so a "compression" that
//     came out larger than the input yields the input instead. That is
//     the rule every other native filter already follows.

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/3rg0n/thlibo/internal/inferd"
)

func init() { RegisterNativeCtx("cordon-filter", cordonFilter) }

// Tunables, read from the environment on every call so an operator can
// change one without restarting the AI client. Names are unchanged from
// the Python so existing CORDON_* settings keep working.
//
// A malformed value falls back to the default rather than failing the
// filter. The Python crashed at import on a bad value, which the script
// dispatcher turned into a passthrough — so the observable outcome was
// "cordon does nothing" either way, and a working default beats silently
// disabling the filter because of a stray character.
func cordonConfig() cordonSettings {
	return cordonSettings{
		WindowSize:    envInt("CORDON_WINDOW_SIZE", 10),
		WindowStride:  envInt("CORDON_WINDOW_STRIDE", 5),
		KNeighbours:   envInt("CORDON_K", 5),
		TopPercentile: envFloat("CORDON_TOP_PERCENTILE", 20),
		MaxWindows:    envInt("CORDON_MAX_WINDOWS", 0), // 0 = no cap
		MaxChars:      envInt("CORDON_MAX_CHARS", 4000),
		BatchSize:     envInt("CORDON_BATCH_SIZE", 32),
		EmbedTimeout:  envDuration("CORDON_EMBED_TIMEOUT", 60*time.Second),
		TotalTimeout:  envDuration("CORDON_TIMEOUT", 30*time.Second),
	}
}

type cordonSettings struct {
	WindowSize    int
	WindowStride  int
	KNeighbours   int
	TopPercentile float64
	MaxWindows    int
	MaxChars      int
	BatchSize     int

	// EmbedTimeout bounds one batch round-trip; TotalTimeout bounds the
	// whole filter across every batch.
	//
	// TotalTimeout is new, and it is not a nicety. As a script processor
	// cordon was bounded at 30s by Dispatcher.ScriptTimeout — its own 60s
	// embed timeout was never reachable. A native filter has no such outer
	// bound (RunNativeCtx cannot interrupt a running filter), so dropping
	// the subprocess without replacing its ceiling would have turned a
	// wedged daemon into a hung PreToolUse hook. 30s reproduces the bound
	// the Python actually ran under.
	EmbedTimeout time.Duration
	TotalTimeout time.Duration
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil {
		return v
	}
	return def
}

func envFloat(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64); err == nil {
		return v
	}
	return def
}

// envDuration reads a float count of seconds, matching the Python's
// CORDON_*_TIMEOUT values (which were floats, not Go duration strings).
// A Go duration string is accepted too, since someone will try it.
func envDuration(name string, def time.Duration) time.Duration {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	if secs, err := strconv.ParseFloat(s, 64); err == nil {
		if secs <= 0 {
			return 0 // explicit "no timeout"
		}
		return time.Duration(secs * float64(time.Second))
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return def
}

// cordonEmbedder is the embedding round-trip cordon needs. Narrowed to
// the one method so the test can supply a deterministic in-process
// embedder — the filter's output is otherwise unpinnable, since a real
// model's vectors are the entire input to the ranking.
type cordonEmbedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float64, error)
}

// cordonEmbedderFor is a package var, not a parameter, because the
// NativeCtxFilter signature has no room for one. Tests swap it; nothing
// else does.
var cordonEmbedderFor = func(timeout time.Duration) cordonEmbedder {
	return &inferd.EmbedClient{Timeout: timeout}
}

func cordonFilter(ctx context.Context, input []byte) []byte {
	cfg := cordonConfig()
	if cfg.WindowSize < 1 || cfg.WindowStride < 1 {
		return input
	}

	lines := cordonNonBlankLines(string(input))
	// Below 2 windows' worth of records there is no "rare" to speak of:
	// every window would be a neighbour of every other.
	if len(lines) < cfg.WindowSize*2 {
		return input
	}

	windows := cordonWindows(lines, cfg)
	if len(windows) < 2 {
		return input
	}
	windows = cordonSample(windows, cfg.MaxWindows)

	if cfg.TotalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.TotalTimeout)
		defer cancel()
	}

	texts := make([]string, len(windows))
	for i, w := range windows {
		texts[i] = w.text
	}
	vectors, err := cordonEmbedAll(ctx, cfg, texts)
	if err != nil {
		cordonDebugf("embed failed: %v; passthrough", err)
		return input
	}

	scores := cordonKNNScores(vectors, cfg.KNeighbours)

	// Top-percentile by score. Ties break toward the earlier window, which
	// keeps the selection stable when a stream has many identical records.
	topN := int(float64(len(scores)) * cfg.TopPercentile / 100.0)
	if topN < 1 {
		topN = 1
	}
	if topN > len(scores) {
		topN = len(scores)
	}
	ranked := make([]int, len(scores))
	for i := range ranked {
		ranked[i] = i
	}
	sort.SliceStable(ranked, func(a, b int) bool { return scores[ranked[a]] > scores[ranked[b]] })
	ranked = ranked[:topN]

	// A line is anomalous if any window containing it ranked. Walk the
	// flagged windows in input order and dedupe, so the emitted lines stay
	// in the order they arrived rather than in score order — the reader is
	// looking at a log, and a reordered log is harder to use than a longer
	// one.
	starts := make([]int, len(ranked))
	for i, r := range ranked {
		starts[i] = windows[r].start
	}
	sort.Ints(starts)

	seen := make(map[int]bool, len(lines))
	flagged := make([]string, 0, len(lines))
	for _, start := range starts {
		for off := 0; off < cfg.WindowSize; off++ {
			idx := start + off
			if idx >= len(lines) || seen[idx] {
				continue
			}
			seen[idx] = true
			flagged = append(flagged, lines[idx])
		}
	}

	return []byte(cordonFormatGroups(flagged, len(lines)))
}

// cordonNonBlankLines splits input into records, dropping blank and
// whitespace-only ones.
//
// Python's str.splitlines() also breaks on \v, \f, \x1c-\x1e, \x85, and
// the Unicode line separators. Splitting on \n only is the narrower
// reading and the right one here: those bytes appear in log *payloads*
// (a \x1b-prefixed colour escape, a form feed in captured terminal
// output) far more often than as record separators, and treating one as a
// line break would split a single record into two signatures.
func cordonNonBlankLines(raw string) []string {
	out := make([]string, 0, 64)
	for _, ln := range strings.Split(raw, "\n") {
		ln = strings.TrimSuffix(ln, "\r")
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

type cordonWindow struct {
	start int // index into the line slice
	text  string
}

// cordonWindows slices the lines into overlapping windows. A stride below
// the window size gives overlap, which makes an anomaly more likely to
// land cleanly inside at least one window instead of straddling two and
// being diluted in both.
func cordonWindows(lines []string, cfg cordonSettings) []cordonWindow {
	if len(lines) < cfg.WindowSize {
		return nil
	}
	var out []cordonWindow
	for start := 0; start+cfg.WindowSize <= len(lines); start += cfg.WindowStride {
		text := strings.Join(lines[start:start+cfg.WindowSize], "\n")
		if cfg.MaxChars > 0 {
			text = cordonTruncateRunes(text, cfg.MaxChars)
		}
		out = append(out, cordonWindow{start: start, text: text})
	}
	return out
}

// cordonTruncateRunes cuts to n characters (not bytes) — the Python slice
// this replaces counted characters, and a byte cut could split a rune in
// the middle and hand invalid UTF-8 to the embedder.
func cordonTruncateRunes(s string, n int) string {
	if len(s) <= n { // bytes >= runes, so this is a safe fast path
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// cordonSample uniformly thins windows down to max, bounding the O(n²)
// pairwise-distance step. Striding rather than truncating keeps coverage
// across the whole input — truncating would make cordon blind to the tail
// of exactly the long logs the cap exists for.
func cordonSample(windows []cordonWindow, max int) []cordonWindow {
	if max <= 0 || len(windows) <= max {
		return windows
	}
	step := float64(len(windows)) / float64(max)
	out := make([]cordonWindow, 0, max)
	for i := 0; i < max; i++ {
		out = append(out, windows[int(float64(i)*step)])
	}
	return out
}

// cordonEmbedAll embeds every window, batched so no single request
// approaches inferd's frame cap. Any batch failing fails the whole call:
// a partial vector set would leave some windows unscored, and scoring the
// rest against a smaller neighbourhood silently changes what counts as
// rare.
func cordonEmbedAll(ctx context.Context, cfg cordonSettings, texts []string) ([][]float64, error) {
	batch := cfg.BatchSize
	if batch < 1 {
		batch = 32
	}
	client := cordonEmbedderFor(cfg.EmbedTimeout)
	out := make([][]float64, 0, len(texts))
	for i := 0; i < len(texts); i += batch {
		end := i + batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := client.Embed(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		if len(vecs) != end-i {
			return nil, fmt.Errorf("cordon: batch at %d returned %d vectors for %d inputs",
				i, len(vecs), end-i)
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// cordonKNNScores returns each vector's mean Euclidean distance to its k
// nearest neighbours. Higher = more isolated = more anomalous.
//
// inferd L2-normalises its embeddings, so cosine distance here reduces to
// Euclidean/√2 — the ranking is identical either way and Euclidean keeps
// the arithmetic readable.
//
// Brute-force O(n²) on purpose. n is the window count (low thousands at
// most, and CORDON_MAX_WINDOWS caps it), and a spatial index would be
// several hundred lines of code to save time this filter does not spend.
func cordonKNNScores(vectors [][]float64, kNeighbours int) []float64 {
	n := len(vectors)
	scores := make([]float64, n)
	k := kNeighbours
	if k > n-1 {
		k = n - 1
	}
	if k <= 0 {
		return scores // single vector: nothing to be far from
	}

	// Reused across rows so the inner loop doesn't allocate per vector.
	dists := make([]float64, 0, n)
	for i := range vectors {
		dists = dists[:0]
		for j := range vectors {
			if j != i {
				dists = append(dists, cordonSquaredDistance(vectors[i], vectors[j]))
			}
		}
		// Partial selection: only the k smallest matter, and k is 5 by
		// default against an n in the thousands.
		sort.Float64s(dists)
		sum := 0.0
		for _, d := range dists[:k] {
			sum += math.Sqrt(d)
		}
		scores[i] = sum / float64(k)
	}
	return scores
}

// cordonSquaredDistance is the squared Euclidean distance between two
// vectors, comparing only their common prefix. Mismatched lengths should
// not happen — one model, one dimensions request — but a short vector
// must not panic a filter that runs inside a PreToolUse hook, and
// ignoring the tail degrades the score rather than losing the output.
func cordonSquaredDistance(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

// cordonGroup is one signature's worth of flagged lines.
type cordonGroup struct {
	sig    string
	level  string
	count  int
	sample string
	lines  []string
}

// cordonFormatGroups groups the flagged lines by signature and emits the
// `compress` output shape: a sig/level/count/sample/keys block per group,
// blank-line separated, then a tail line recording the reduction.
func cordonFormatGroups(lines []string, totalInputLines int) string {
	index := map[string]int{}
	var groups []*cordonGroup
	for _, line := range lines {
		sig := cordonSignature(line)
		if at, ok := index[sig]; ok {
			groups[at].count++
			groups[at].lines = append(groups[at].lines, line)
			continue
		}
		index[sig] = len(groups)
		groups = append(groups, &cordonGroup{
			sig:    sig,
			level:  cordonLevel(line),
			count:  1,
			sample: cordonSample240(line),
			lines:  []string{line},
		})
	}

	// Severity first, then frequency, then signature. The signature
	// tiebreak makes the order total (signatures are the group keys, so
	// they're unique), which is what keeps the output stable enough to
	// diff between runs.
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if ra, rb := cordonLevelRank[a.level], cordonLevelRank[b.level]; ra != rb {
			return ra > rb
		}
		if a.count != b.count {
			return a.count > b.count
		}
		return a.sig < b.sig
	})

	var sb strings.Builder
	for _, g := range groups {
		sb.WriteString("sig=" + g.sig + "\n")
		sb.WriteString("level=" + g.level + "\n")
		sb.WriteString("count=" + strconv.Itoa(g.count) + "\n")
		sb.WriteString("sample=" + g.sample + "\n")
		sb.WriteString("keys=" + strings.Join(cordonGroupKeys(g.lines), ",") + "\n")
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("tail=%d→%d\n", totalInputLines, len(groups)))
	return sb.String()
}

// cordonSample240 is the group's representative line, capped at 240
// characters with an ellipsis. Characters, not bytes — a byte cut would
// split a rune.
func cordonSample240(line string) string {
	const limit = 240
	cut := cordonTruncateRunes(line, limit)
	if len(cut) < len(line) {
		return cut + "…"
	}
	return line
}

// cordonGroupKeys collects the load-bearing tokens seen anywhere in the
// group — paths, status codes, error codes, versions — deduped, in first
// appearance order, capped at 12. The cap is what keeps a group of a
// thousand near-identical lines from emitting a thousand keys and undoing
// the compression.
func cordonGroupKeys(lines []string) []string {
	const maxKeys = 12
	seen := map[string]bool{}
	keys := make([]string, 0, maxKeys)
	for _, line := range lines {
		for _, tok := range cordonExtractKeys(line) {
			if !seen[tok] {
				seen[tok] = true
				keys = append(keys, tok)
			}
			if len(keys) >= maxKeys {
				return keys
			}
		}
	}
	return keys
}

// cordonDebugf writes to stderr when CORDON_DEBUG is set. stderr, never
// stdout: a native filter's return value IS the tool output, so a
// diagnostic written into it would replace the document rather than
// annotate it (invariant #2).
func cordonDebugf(format string, args ...any) {
	if os.Getenv("CORDON_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "cordon: "+format+"\n", args...)
}
