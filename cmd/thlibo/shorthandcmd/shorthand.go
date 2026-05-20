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

	inferd "github.com/3rg0n/inferd/clients/go"
	"github.com/3rg0n/thlibo/cmd/thlibo/compresscmd"
	"github.com/3rg0n/thlibo/internal/inferdcli"
	"github.com/3rg0n/thlibo/internal/processors"
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
		yamlMode string
	)
	fs.BoolVar(&inPlace, "in-place", false, "rewrite the file in place; original saved to <file>.orig")
	fs.BoolVar(&validate, "validate", false, "run the eval checklist on the file's existing content; report and exit 0/1 without modifying")
	fs.BoolVar(&quiet, "quiet", false, "suppress the reduction-summary line on stderr")
	fs.BoolVar(&noBackup, "no-backup", false, "with --in-place, skip the .orig backup (use when you have version control)")
	fs.StringVar(&yamlMode, "yaml", "auto", "YAML-aware mode: auto (detect), on (force), off (disable). Auto detects YAML by file extension and content shape.")
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

	// Try to build the engine. If the daemon is unavailable, fall
	// back to emitting the original bytes — same fail-closed
	// contract as eval-fail and short-circuit. Empty stdout +
	// non-zero exit on the unhappy path used to truncate files
	// when wired into `thlibo shorthand --in-place foo.md` from a
	// pre-commit hook with no daemon running.
	engine, buildErr := buildEngine()
	if buildErr != nil {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: backend unavailable; emitting original:", buildErr)
		return emitOriginal(target, string(raw), inPlace, noBackup, quiet)
	}

	useYAML := shouldUseYAMLMode(yamlMode, target, string(raw))
	var res *shorthand.Result
	if useYAML {
		res, err = engine.CompressYAML(context.Background(), string(raw))
	} else {
		res, err = engine.Compress(context.Background(), string(raw))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: compression failed; emitting original:", err)
		return emitOriginal(target, string(raw), inPlace, noBackup, quiet)
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

// shouldUseYAMLMode decides whether to dispatch through the YAML-
// aware walker. mode=on/off forces; mode=auto looks at the file
// extension first (cheap signal), then the content shape (more
// reliable but pays a SplitN). The content sniff catches files
// without extensions and rules in stdin.
func shouldUseYAMLMode(mode, path, content string) bool {
	switch mode {
	case "on":
		return true
	case "off":
		return false
	}
	if path != "" && path != "-" {
		switch {
		case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
			return true
		}
	}
	return shorthand.IsYAMLContent(content)
}

// readInput slurps the file or stdin into a byte slice. "-" is the
// stdin convention shared with thlibo compress.
func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path) // #nosec G304 -- argv-supplied path is the user's own input file
}

// buildEngine wires a shorthand.Engine pointed at inferd's
// "shorthand" prompt processor. The engine package is transport-
// agnostic; this is the production wiring.
func buildEngine() (*shorthand.Engine, error) {
	client := &inferdcli.Client{Address: inferdcli.DefaultInferenceAddress()}
	pr := &daemonBackend{client: client}
	return &shorthand.Engine{Backend: pr}, nil
}

// emitOriginal honours the fail-closed contract: when the daemon
// is unreachable or the compression run errors, the user gets the
// original bytes back (stdout or in-place rewrite of identical
// content) rather than an empty file. Callers print a stderr
// warning explaining WHY they fell back before invoking this.
//
// In --in-place mode the file is left untouched (we'd be writing
// the same bytes back; skipping the write avoids a bogus mtime
// bump). Stdout mode emits the bytes directly.
func emitOriginal(target, raw string, inPlace, _ bool, quiet bool) int {
	if inPlace {
		// File on disk is already the original bytes — nothing to
		// write. Skip the .orig backup too; there's no new content
		// to back up against.
		if !quiet {
			fmt.Fprintln(os.Stderr, "thlibo shorthand: --in-place no-op (original bytes preserved)")
		}
		return ExitOK
	}
	if _, err := io.WriteString(os.Stdout, raw); err != nil {
		fmt.Fprintln(os.Stderr, "thlibo shorthand: stdout:", err)
		return ExitWriteFailed
	}
	return ExitOK
}

// daemonBackend adapts the inferd client + shorthand processor
// descriptor to the shorthand.Backend interface. It loads the
// embedded processor registry on first use; every call after that
// reuses the cached descriptor for the inferd round-trip.
type daemonBackend struct {
	client *inferdcli.Client
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

	req := inferd.Request{
		ID: "shorthand-" + b.desc.Name,
		Messages: []inferd.Message{
			{Role: inferd.RoleSystem, Content: b.desc.SystemPrompt},
			{Role: inferd.RoleUser, Content: input},
		},
	}
	if b.desc.Temperature != nil {
		req.Temperature = b.desc.Temperature
	}
	if b.desc.MaxTokens != nil {
		req.MaxTokens = b.desc.MaxTokens
	}
	stream := false
	req.Stream = &stream

	out, err := b.client.Post(ctx, req)
	if err != nil {
		return "", err
	}
	// Strip the Gemma <thought> block + leading/trailing whitespace
	// the model sometimes adds.
	return strings.TrimSpace(processors.Strip(out)), nil
}
