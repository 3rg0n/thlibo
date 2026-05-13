// Package pullcmd implements `thlibo pull`.
//
// Downloads a GGUF model from its pinned URL, verifies SHA-256,
// and places it in ~/.thlibo/models/ (or $THLIBO_MODELS_DIR).
//
// Exit codes:
//
//	0   success (downloaded or already present with matching hash)
//	2   bad flags / argv
//	3   cannot resolve home dir / write to models dir
//	4   unknown model name
//	5   URL validation failure
//	6   SHA mismatch
//	7   network/IO error during download
package pullcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/3rg0n/thlibo/internal/install"
)

// Exit codes kept named so shell scripts and tests can key on them.
const (
	ExitOK           = 0
	ExitUsage        = 2
	ExitDirError     = 3
	ExitUnknownModel = 4
	ExitBadURL       = 5
	ExitSHA          = 6
	ExitNetwork      = 7
	ExitUnpinned     = 8
)

// knownModels maps user-facing names to Model structs. v0.1 has one
// entry; keeping a table so `thlibo pull` can grow alternatives
// (Q8_0 GPU quant, larger variants) without restructuring.
var knownModels = map[string]install.Model{
	"gemma-4-e4b":        install.DefaultModel,
	"gemma-4-e4b-q4_k_m": install.DefaultModel,
}

// Run drives one invocation. argv is everything after `thlibo pull`.
func Run(argv []string) int {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	var (
		dir           string
		allowUnpinned bool
		quiet         bool
		noResume      bool
	)
	fs.StringVar(&dir, "dir", "", "destination dir (default: $THLIBO_MODELS_DIR or ~/.thlibo/models)")
	fs.BoolVar(&allowUnpinned, "allow-unpinned", false, "download a model whose SHA256 is not pinned yet")
	fs.BoolVar(&quiet, "quiet", false, "suppress progress output")
	fs.BoolVar(&noResume, "no-resume", false, "ignore any existing .part file and redownload from scratch")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `thlibo pull — download a GGUF model

Usage:
  thlibo pull [flags] [<name>]

Available models:
`)
		for name := range knownModels {
			fmt.Fprintf(os.Stderr, "  %s\n", name)
		}
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}

	name := "gemma-4-e4b"
	if fs.NArg() >= 1 {
		name = fs.Arg(0)
	}

	model, ok := knownModels[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "thlibo pull: unknown model %q\n", name)
		return ExitUnknownModel
	}

	// Context that honors SIGINT so Ctrl-C interrupts a long download.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nthlibo pull: interrupted")
		cancel()
	}()

	opts := install.PullOptions{
		Dir:           dir,
		AllowUnpinned: allowUnpinned,
		NoResume:      noResume,
	}
	if !quiet {
		opts.Progress = newTerminalProgress()
	}

	path, err := install.Pull(ctx, model, opts)
	if err != nil {
		// Map structured errors to exit codes so scripts can
		// distinguish transient network failures (retry) from
		// fatal config mistakes (don't retry).
		fmt.Fprintln(os.Stderr, "thlibo pull:", err)
		switch {
		case errors.Is(err, context.Canceled):
			return ExitNetwork
		case strings.Contains(err.Error(), "sha256 mismatch"):
			return ExitSHA
		case strings.Contains(err.Error(), "no pinned SHA256"):
			return ExitUnpinned
		case strings.Contains(err.Error(), "URL") || strings.Contains(err.Error(), "url"):
			return ExitBadURL
		case strings.Contains(err.Error(), "models dir"):
			return ExitDirError
		default:
			return ExitNetwork
		}
	}

	if !quiet {
		fmt.Printf("downloaded: %s\n", path)
	}
	return ExitOK
}

// newTerminalProgress returns a ProgressFunc that prints a single
// carriage-return-updated line to stderr so we don't pollute stdout
// (which the release bundle's install scripts consume).
func newTerminalProgress() install.ProgressFunc {
	start := time.Now()
	return func(written, total int64) {
		elapsed := time.Since(start)
		var pct string
		if total > 0 {
			pct = fmt.Sprintf("%3d%%", int((written*100)/total))
		} else {
			pct = "   ?"
		}
		speed := ""
		if elapsed.Seconds() > 0 {
			mbps := float64(written) / elapsed.Seconds() / (1 << 20)
			speed = fmt.Sprintf(" %.1f MiB/s", mbps)
		}
		fmt.Fprintf(os.Stderr, "\rthlibo pull: %s %s%s        ", pct, humanBytes(written), speed)
	}
}

// humanBytes formats a byte count as a compact, right-aligned-ish
// string. Anything >= 1 MiB prints as MiB; otherwise KiB.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
