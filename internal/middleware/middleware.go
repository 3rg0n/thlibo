// Package middleware is the heart of the thlibo binary: it reads tool
// output, decides whether to short-circuit, matches a fast-path
// processor, calls the daemon router for anything else, dispatches
// through one or more processors, and returns the result.
//
// The cardinal rule is in §thlibo responsibility and every gate row
// B8*: on ANY error path, return the original input unchanged. The
// middleware must never break the AI client.
package middleware

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	builtins "github.com/3rg0n/thlibo/processors"

	"github.com/3rg0n/thlibo/internal/inferd"
	"github.com/3rg0n/thlibo/internal/processors"
	"github.com/3rg0n/thlibo/internal/promptsan"
	"github.com/3rg0n/thlibo/internal/router"
)

// BuildRegistry constructs the middleware's processor registry using
// the built-in processors embedded at compile time and optionally an
// on-disk user directory. The embedded FS has its processor folders
// at the root, matching what BuildFromSources expects. Returns the
// registry plus any non-fatal warnings (quarantined descriptors).
// See gate rows C4, C5.
func BuildRegistry(userDir string) (*processors.Registry, []error, error) {
	sources := []processors.Source{
		{FS: builtins.FS, Origin: processors.OriginBuiltin},
	}
	if userDir != "" {
		abs, err := filepath.Abs(userDir)
		if err != nil {
			return nil, nil, err
		}
		// Only include the user source if the directory exists;
		// otherwise Scan would log a read-root warning.
		if _, err := os.Stat(abs); err == nil {
			sources = append(sources, processors.Source{
				FS:       os.DirFS(abs),
				DiskRoot: abs,
				Origin:   processors.OriginUser,
			})
		}
	}
	return processors.BuildFromSources(sources...)
}

// MinBytesForRouting is the spec's short-circuit threshold: tool
// outputs below this size pass through without any scanning or
// routing (B1).
const MinBytesForRouting = 2000

// Pipeline carries the pieces the middleware needs at request time.
// Built once at startup by the adapter (claudecode, codex, proxy)
// and reused per call.
type Pipeline struct {
	Registry     *processors.Registry
	Router       RouterClient
	Dispatcher   *processors.Dispatcher
	OnWarning    func(string) // optional: log quarantined processors etc.
}

// RouterClient is what the middleware calls to decide routing. The
// router package's Ask function fits this shape; tests pass in a fake.
type RouterClient interface {
	Ask(ctx context.Context, reg *processors.Registry, input string) (router.Decision, error)
}

// Process reads tool output from in, applies the pipeline, and writes
// the result to out. Return value is always nil: any internal error
// falls back to passing in's bytes through. Adapters that need error
// signalling should capture warnings via Pipeline.OnWarning.
//
// The contract is "always succeed, always produce output" because
// Claude Code treats a non-zero exit from a hook as an error banner
// in the user's UI.
func (p *Pipeline) Process(ctx context.Context, in io.Reader, out io.Writer) error {
	raw, err := readAll(in)
	if err != nil {
		// Can't even read input -> write nothing, return nil.
		return nil
	}
	result := p.decide(ctx, raw)
	_, _ = out.Write([]byte(result))
	return nil
}

func (p *Pipeline) decide(ctx context.Context, raw string) string {
	// B1: short-circuit small inputs.
	if len(raw) < MinBytesForRouting {
		return raw
	}

	// B8h: no processors -> passthrough.
	if p.Registry == nil || p.Registry.Len() == 0 {
		return raw
	}

	// B4: fast-path regex. A hit dispatches immediately without a
	// daemon call.
	if d := p.Registry.MatchFastPath(raw); d != nil {
		out, err := p.Dispatcher.Run(ctx, d, raw)
		if err != nil {
			p.warn("fast-path " + d.Name + ": " + err.Error())
			return raw // B8d/B8e fallback
		}
		// Cordon fallback (#28): ndjson-filter groups by signature and
		// can over-collapse an access log (every line the same
		// level+msg) down to a couple of rows, hiding the outliers a
		// model would want. When its output looks over-collapsed, run
		// the semantic-anomaly surfacer over the ORIGINAL input and keep
		// its result only if it's a strict improvement. cordon-filter
		// fails open (input verbatim) when inferd's embed socket is
		// unreachable, so this never breaks the client — worst case we
		// keep ndjson's output.
		if d.Name == "ndjson-filter" && overCollapsed(raw, out) {
			if better := p.cordonFallback(ctx, raw, out); better != "" {
				return better
			}
		}
		return out
	}

	// B5/B6/B7: routing call.
	decision, err := p.Router.Ask(ctx, p.Registry, raw)
	if err != nil {
		// B8a/B8b: daemon unreachable or timeout -> passthrough.
		p.warn("router: " + err.Error())
		return raw
	}
	if decision.Passthrough() {
		return raw // B6: "none"
	}

	out, err := p.Dispatcher.RunChain(ctx, p.Registry, decision.Chain, raw)
	if err != nil {
		p.warn("chain " + joinNames(decision.Chain) + ": " + err.Error())
		return raw // B8d/B8e/B8f fallback
	}
	return out
}

func (p *Pipeline) warn(msg string) {
	if p.OnWarning != nil {
		p.OnWarning(msg)
	}
}

// CordonMinInputLines is the floor below which the cordon fallback never
// fires — anomaly surfacing over a handful of lines is pointless and the
// embed round-trip isn't worth it.
const CordonMinInputLines = 30

// overCollapsed reports whether ndjson-filter's output looks like it
// discarded meaningful variation — the #27 failure mode where an access
// log with a constant level+msg collapses to a couple of rows. The
// heuristic (issue #28 option C): a non-trivial input (>= CordonMinInput
// Lines) whose structured output is < 3 groups, OR whose group-to-input
// ratio is under 5%. Both signal "the signature couldn't tell these
// records apart", which is exactly cordon's job.
func overCollapsed(raw, out string) bool {
	inLines := countNonEmptyLines(raw)
	if inLines < CordonMinInputLines {
		return false
	}
	outLines := countNonEmptyLines(out)
	if outLines < 3 {
		return true
	}
	return float64(outLines)/float64(inLines) < 0.05
}

func countNonEmptyLines(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// cordonFallback runs the cordon-filter processor over the original input
// and returns its output only if it is a strict improvement over the
// ndjson output (ndjsonOut) — i.e. non-empty, different from the raw
// input (cordon fails open to verbatim when inferd's embed socket is
// unreachable), and smaller than the raw input. Returns "" to signal
// "keep the ndjson output". Never errors out of the pipeline.
func (p *Pipeline) cordonFallback(ctx context.Context, raw, ndjsonOut string) string {
	if p.Registry == nil {
		return ""
	}
	d := p.Registry.Get("cordon-filter")
	if d == nil {
		return "" // not installed (e.g. mirrored dir without it)
	}
	out, err := p.Dispatcher.Run(ctx, d, raw)
	if err != nil {
		p.warn("cordon fallback: " + err.Error())
		return ""
	}
	// cordon failed open (embed unreachable) → output == raw verbatim.
	// In that case there's no gain; keep ndjson's collapse.
	if out == raw || strings.TrimSpace(out) == "" {
		return ""
	}
	// Only prefer cordon if it actually surfaced a more informative view
	// than ndjson's over-collapsed output. We treat "more lines than the
	// collapsed output, still well under the raw input" as the win: it
	// restored discarded variation without regressing compression.
	if countNonEmptyLines(out) > countNonEmptyLines(ndjsonOut) && len(out) < len(raw) {
		return out
	}
	return ""
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}

// readAll is io.ReadAll with a large but bounded buffer to avoid
// pathological inputs holding a process hostage. 64 MiB is far beyond
// any realistic tool output.
func readAll(r io.Reader) (string, error) {
	const maxBytes = 64 << 20
	b, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// PromptRunner adapts an inferd.Client into the
// processors.PromptRunner interface so prompt processors can call
// inferd through the same transport as the router.
type PromptRunner struct {
	Client *inferd.Client
}

// Run sends a prompt processor invocation to inferd and returns the
// model's answer with thought blocks stripped (see spec
// §"Thought-stripping"). Gemma 4 always emits a thought block - even
// empty when thinking is disabled - so stripping runs unconditionally.
func (p *PromptRunner) Run(ctx context.Context, d *processors.Descriptor, input string) (string, error) {
	if p.Client == nil {
		return "", errors.New("middleware: no inferd client")
	}
	// Escape Gemma native tool-call markers in tool output before
	// it becomes a user turn. Real git/npm/cargo output does not
	// contain these sequences; if they do appear, they are attacker-
	// controlled (e.g. a crafted commit diff or README). See
	// THREAT_MODEL.md finding #1.
	req := inferd.Request{
		ID: "prompt-" + d.Name,
		Messages: []inferd.Message{
			{Role: inferd.RoleSystem, Content: d.SystemPrompt},
			{Role: inferd.RoleUser, Content: promptsan.Sanitize(input)},
		},
	}
	if d.Temperature != nil {
		req.Temperature = d.Temperature
	}
	if d.TopP != nil {
		req.TopP = d.TopP
	}
	if d.TopK != nil {
		req.TopK = d.TopK
	}
	if d.MaxTokens != nil {
		req.MaxTokens = d.MaxTokens
	}
	s := false
	req.Stream = &s

	out, err := p.Client.Post(ctx, req)
	if err != nil {
		return "", err
	}
	return processors.Strip(out.Text), nil
}
