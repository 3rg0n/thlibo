// Package casecmd implements `thlibo case <file>`.
//
// Produces ~/.thlibo/cases/<timestamp>-<hash>/ containing:
//
//	compressed.log    — source run through the middleware
//	summary.md        — human-readable header
//	meta.json         — machine-readable record
//
// On success, prints the case directory path on stdout so callers
// can pipe it: `thlibo case huge.log | xargs -I{} cat {}/summary.md`.
//
// Exit codes:
//
//	0   success (a case directory was created)
//	2   bad flags / argv
//	3   source file missing or not readable
//	4   source file is not a regular file
//	5   write failure (disk full, permission denied)
//	7   --prune failed
package casecmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/3rg0n/thlibo/cmd/thlibo/compresscmd"
	"github.com/3rg0n/thlibo/internal/casefile"
	"github.com/3rg0n/thlibo/internal/logx"
	"github.com/3rg0n/thlibo/internal/version"
)

// Exit codes — stable so scripts can key on them.
const (
	ExitOK            = 0
	ExitUsage         = 2
	ExitSourceMissing = 3
	ExitNotRegular    = 4
	ExitWriteFailed   = 5
	// ExitLowValue — case was created but compressed.log carries no
	// usable signal (e.g. a scanned PDF where every page produced an
	// "OCR not yet supported" placeholder). The case dir is still
	// written to disk for forensics, but stdout stays empty so the
	// Read PreToolUse hook treats this as "no match" and lets the
	// original read pass through to Claude Code's native reader.
	// See issue #31.
	ExitLowValue    = 6
	ExitPruneFailed = 7
)

// Run is the subcommand entry. argv is everything after "thlibo case".
func Run(argv []string) int {
	fs := flag.NewFlagSet("case", flag.ContinueOnError)
	var (
		casesDir string
		quiet    bool
		pruneAge time.Duration
	)
	fs.StringVar(&casesDir, "dir", "", "override cases dir (default: $THLIBO_CASES_DIR or ~/.thlibo/cases)")
	fs.BoolVar(&quiet, "quiet", false, "suppress the human-readable summary on stdout (still prints the case dir path)")
	fs.DurationVar(&pruneAge, "prune", 0, "prune cases older than this duration, then exit (e.g. 168h)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `thlibo case — create a compressed "case" for a large log file

Usage:
  thlibo case [flags] <file>
  thlibo case --prune <duration>

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}

	if casesDir == "" {
		casesDir = os.Getenv("THLIBO_CASES_DIR")
	}

	log := logx.New("thlibo-case", "", 0)
	defer log.Close()

	if pruneAge > 0 {
		n, err := casefile.Prune(casesDir, pruneAge,
			time.Now().UTC(),
			func(format string, args ...any) {
				log.Warn("prune_item_failed", logx.Str("detail", fmt.Sprintf(format, args...)))
			})
		if err != nil {
			fmt.Fprintln(os.Stderr, "thlibo case: prune:", err)
			return ExitPruneFailed
		}
		fmt.Printf("pruned %d case directories older than %s\n", n, pruneAge)
		return ExitOK
	}

	if fs.NArg() != 1 {
		fs.Usage()
		return ExitUsage
	}
	source := fs.Arg(0)

	// Pipeline is best-effort: if the daemon is down we still
	// produce a case directory with compressed.log == source. The
	// Read hook that calls us would rather have a "case" the user
	// can navigate than fail outright and break a drop-in read.
	p, pipelineErr := compresscmd.BuildPipeline()
	if pipelineErr != nil {
		log.Warn("pipeline_unavailable", logx.Err(pipelineErr))
	}
	if p != nil {
		p.ToolName = "Read" // case is the Read-hook path; telemetry label only
		// Flush telemetry before this short-lived process exits (ADR 0011).
		defer p.Shutdown(context.Background())
	}

	res, err := casefile.Create(context.Background(), source, casefile.Options{
		CasesRoot:     casesDir,
		ThliboVersion: version.Tag,
		Pipeline:      p,
		OCR:           pdfOCRFunc(),
	})
	if err != nil {
		switch {
		case os.IsNotExist(err) || errorsContains(err, "stat source"):
			fmt.Fprintln(os.Stderr, "thlibo case: source not found:", source)
			return ExitSourceMissing
		case errors.Is(err, casefile.ErrSourceNotRegular):
			fmt.Fprintln(os.Stderr, "thlibo case:", err)
			return ExitNotRegular
		default:
			fmt.Fprintln(os.Stderr, "thlibo case:", err)
			return ExitWriteFailed
		}
	}

	log.Info("case_created",
		logx.Str("id", res.Meta.ID),
		logx.Int64("source_bytes", res.Meta.SourceSize),
		logx.Int64("compressed_bytes", res.Meta.CompressedSize),
		logx.Any("reduction_pct", res.Meta.ReductionPercent),
		logx.Bool("fallback", res.Meta.Fallback),
		logx.Bool("low_value", res.Meta.LowValue),
	)

	// Low-value short-circuit. The case is on disk for forensics
	// (so an operator can see what we got), but stdout stays empty
	// and we exit non-zero so the Read PreToolUse hook treats this
	// as no-match and lets Claude's native reader handle the file.
	// See issue #31.
	if res.Meta.LowValue {
		if !quiet {
			fmt.Fprintf(os.Stderr,
				"thlibo case: %s\n  source:     %d bytes\n  compressed: %d bytes\n  note: low-value output (e.g. scanned PDF without OCR); upstream reader will handle the original.\n",
				res.Dir, res.Meta.SourceSize, res.Meta.CompressedSize)
		}
		return ExitLowValue
	}

	// Primary output is the case dir path so the hook (and shell
	// pipelines) can parse it trivially.
	fmt.Println(res.Dir)
	if !quiet {
		fmt.Fprintf(os.Stderr,
			"thlibo case: %s\n  source:     %d bytes\n  compressed: %d bytes (-%.1f%%)\n",
			res.Dir, res.Meta.SourceSize, res.Meta.CompressedSize, res.Meta.ReductionPercent)
		if res.Meta.Fallback {
			fmt.Fprintln(os.Stderr, "  note: pipeline unavailable; compressed.log is a verbatim copy")
		}
	}
	return ExitOK
}

// errorsContains is errors.Is but for unwrapped error-message
// substring checks. Keeps the import list small (no strings import
// for one call).
func errorsContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
