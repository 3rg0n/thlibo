// Package shorthandcmd implements `thlibo shorthand`.
//
// Compresses LLM-facing prose (SKILL.md, CLAUDE.md, agents.md, system
// prompts) into token-efficient shorthand while preserving every
// directive, schema, and proper noun verbatim. Modes:
//
//	thlibo shorthand <file>              -> stdout, original untouched
//	thlibo shorthand --in-place <file>   -> overwrite; backup at <f>.orig
//	thlibo shorthand --validate <file>   -> eval-only, exit 0/1
//	thlibo shorthand -                   -> stdin -> stdout
//
// Eval checklist is fail-closed: any required directive / code
// fence / schema / URL / path / version that doesn't survive the
// compression flips the output back to the original input. Better
// to over-emit the original than to silently lose a NEVER directive.
//
// Exit codes:
//
//	0   success (or --validate passed)
//	2   bad flags / argv
//	3   read failed
//	4   write failed
//	5   backend (daemon) unavailable
//	6   eval failed (--validate mode only; in normal mode we
//	    fail-closed and exit 0 with a stderr warning)
package shorthandcmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/3rg0n/thlibo/cmd/thlibo/compresscmd"
	"github.com/3rg0n/thlibo/internal/ipc"
	"github.com/3rg0n/thlibo/internal/processors"
	"github.com/3rg0n/thlibo/internal/router"
	"github.com/3rg0n/thlibo/internal/shorthand"
)

// Exit codes — stable so scripts can key on them.
const (
	ExitOK              = 0
	ExitUsage           = 2
	ExitReadFailed      = 3
	ExitWriteFailed     = 4
	ExitBackendDown     = 5
	ExitValidateFailed  = 6
)

// Run is the subcommand entry. argv is everything after "thlibo shorthand".
func Run(argv []string) int {
	fs := flag.NewFlagSet("shorthand", flag.ContinueOnError)
	var (
		inPlace  bool
		validate bool
		quiet    bool
		noBackup bool
	)
	fs.BoolVar(&inPlace, "in-place", false, "rewrite the file in place; original saved to <file>.orig")
	fs.BoolVar(&validate, "validate", false, "run the eval checklist on the file's existing content; report and exit 0/1 without modifying")
	fs.BoolVar(&quiet, "quiet", false, "suppress the reduction-summary line on stderr")
	fs.BoolVar(&noBackup, "no-backup", false, "with --in-place, skip the .orig backup (use when you have version control)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `thlibo shorthand — compress LLM-facing prose

Usage:
  thlibo shorthand <file>             # write to stdout, original untouched
  thlibo shorthand --in-place <file>  # rewrite in place, backup at <file>.orig
  thlibo shorthand --validate <file>  # eval the existing file, exit 0/1
  thlibo shorthand -                  # read stdin, write stdout

Eval checklist is fail-closed: every NEVER/MUST/DO NOT directive,
code fence, frontmatter key, URL, file path, version string, and
numeric threshold must survive the compression. If any check fails
the original bytes are emitted with a stderr warning.

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return ExitUsage
	}
	target := fs.Arg(0)

	// --validate is incompatible with stdin — there's no file to
	// validate against, just bytes that may or may not pass.
	if validate && target == "-" {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: --validate requires a file path")
		return ExitUsage
	}

	// Load.
	raw, err := readInput(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: read:", err)
		return ExitReadFailed
	}
	if len(raw) == 0 {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: empty input; nothing to do")
		return ExitOK
	}

	// --validate is a read-only path: check the file's own bytes
	// against itself for the must-preserve tokens. Useful as a
	// gating step in CI: "is this SKILL.md self-consistent?".
	if validate {
		failures := shorthand.Evaluate(string(raw), string(raw))
		if len(failures) > 0 {
			fmt.Fprintln(os.Stderr, "validate: failures:")
			for _, f := range failures {
				fmt.Fprintln(os.Stderr, "  -", f)
			}
			return ExitValidateFailed
		}
		if !quiet {
			fmt.Fprintln(os.Stderr, "validate: OK")
		}
		return ExitOK
	}

	engine, err := buildEngine()
	if err != nil {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: backend unavailable:", err)
		return ExitBackendDown
	}

	res, err := engine.Compress(context.Background(), string(raw))
	if err != nil {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: compression failed:", err)
		return ExitBackendDown
	}

	// Surface the eval failures (if any) on stderr so the user knows
	// why we fell back. The exit code stays 0 — fail-closed means
	// the user always gets readable bytes, never a half-compressed
	// surprise.
	if !res.Safe() && !res.AlreadyShorthand {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: eval failed; emitting original:")
		for _, f := range res.EvalFailures {
			fmt.Fprintln(os.Stderr, "  -", f)
		}
	}

	out := res.Compressed

	if inPlace {
		if target == "-" {
			fmt.Fprintln(os.Stderr, "thlibo shorthand: --in-place requires a file path")
			return ExitUsage
		}
		// Atomic-ish: write .orig first, then the new content.
		if !noBackup {
			if err := os.WriteFile(target+".orig", raw, 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "thlibo shorthand: backup:", err)
				return ExitWriteFailed
			}
		}
		if err := os.WriteFile(target, []byte(out), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "thlibo shorthand: write:", err)
			return ExitWriteFailed
		}
	} else {
		if _, err := io.WriteString(os.Stdout, out); err != nil {
			fmt.Fprintln(os.Stderr, "thlibo shorthand: stdout:", err)
			return ExitWriteFailed
		}
	}

	if !quiet {
		switch {
		case res.AlreadyShorthand:
			fmt.Fprintln(os.Stderr, "thlibo shorthand: input already shorthand-shaped; no change")
		case !res.Safe():
			// Already announced above.
		default:
			fmt.Fprintf(os.Stderr,
				"thlibo shorthand: %d -> %d bytes (-%.1f%%)\n",
				len(raw), len(out), res.ReductionPercent)
		}
	}
	return ExitOK
}

// readInput slurps the file or stdin into a byte slice. "-" is the
// stdin convention shared with thlibo compress.
func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path) // #nosec G304 -- argv-supplied path is the user's own input file
}

// buildEngine wires a shorthand.Engine pointed at the daemon's
// "shorthand" prompt processor. The engine package is transport-
// agnostic; this is the production wiring.
func buildEngine() (*shorthand.Engine, error) {
	client := &router.DaemonClient{Address: ipc.DefaultInferenceAddress()}
	pr := &daemonBackend{client: client}
	return &shorthand.Engine{Backend: pr}, nil
}

// daemonBackend adapts the router.DaemonClient + shorthand processor
// descriptor to the shorthand.Backend interface. It loads the
// embedded processor registry on first use; every call after that
// reuses the cached descriptor for the daemon round-trip.
type daemonBackend struct {
	client *router.DaemonClient
	desc   *processors.Descriptor
}

func (b *daemonBackend) Run(ctx context.Context, input string) (string, error) {
	if b.desc == nil {
		// Only need the registry to find the shorthand processor's
		// descriptor; reuse compresscmd.BuildPipeline so we don't
		// duplicate the ~/.thlibo/processors loader.
		p, err := compresscmd.BuildPipeline()
		if err != nil {
			return "", fmt.Errorf("registry: %w", err)
		}
		d := p.Registry.Get("shorthand")
		if d == nil {
			return "", fmt.Errorf("processors: 'shorthand' not registered (run `thlibo install` to mirror built-ins)")
		}
		b.desc = d
	}

	pr := &daemonPromptRunner{client: b.client}
	out, err := pr.Run(ctx, b.desc, input)
	if err != nil {
		return "", err
	}
	// Strip leading/trailing whitespace the model sometimes adds.
	return strings.TrimSpace(out), nil
}

// daemonPromptRunner is a thin wrapper around the router.DaemonClient
// that submits a single prompt-processor request and returns the
// model's response. Same protocol middleware.PromptRunner uses, but
// shorthandcmd doesn't need the full middleware pipeline (no
// processor chain, no fast-path regex), so we hit the daemon
// directly.
type daemonPromptRunner struct {
	client *router.DaemonClient
}

func (p *daemonPromptRunner) Run(ctx context.Context, d *processors.Descriptor, input string) (string, error) {
	req := ipc.Request{
		ID: "shorthand-" + d.Name,
		Messages: []ipc.Message{
			{Role: ipc.RoleSystem, Content: d.SystemPrompt},
			{Role: ipc.RoleUser, Content: input},
		},
	}
	if d.Temperature != nil {
		req.Temperature = d.Temperature
	}
	if d.MaxTokens != nil {
		req.MaxTokens = d.MaxTokens
	}
	stream := false
	req.Stream = &stream

	out, _, err := p.client.Post(ctx, req)
	if err != nil {
		return "", err
	}
	return processors.Strip(out), nil
}
