// Package compresscmd implements `thlibo compress`.
//
// Reads raw tool output from stdin, runs it through the middleware
// (short-circuit → fast-path → router → processor), writes the
// compressed form to stdout. Preserves all fallback semantics: if
// the pipeline fails, the original bytes are emitted verbatim —
// same contract `thlibo exec` enforces after running a subprocess.
//
// Used by the Codex PostToolUse hook (it gets the tool output in
// its stdin envelope rather than by executing a command) and by
// ad-hoc scripts: `cat big.log | thlibo compress > small.log`.
package compresscmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/3rg0n/thlibo/internal/ipc"
	"github.com/3rg0n/thlibo/internal/middleware"
	"github.com/3rg0n/thlibo/internal/processors"
	"github.com/3rg0n/thlibo/internal/router"
)

// Run reads stdin, compresses, writes stdout. Returns 0 on success
// (including passthrough) or 1 on stdin read failure. Never returns
// non-zero for compression-pipeline errors — the fallback contract
// requires the original bytes go out regardless.
func Run(argv []string) int {
	_ = argv // no flags in v0.1

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "thlibo compress: read stdin:", err)
		return 1
	}

	p, err := buildPipeline()
	if err != nil {
		// No pipeline means no daemon client; still emit raw so
		// the caller sees the original output rather than nothing.
		_, _ = os.Stdout.Write(raw)
		return 0
	}

	// Pipeline.Process handles every fallback internally and
	// always writes something, so we can ignore its error.
	_ = p.Process(context.Background(), bytes.NewReader(raw), os.Stdout)
	return 0
}

// buildPipeline mirrors execcmd.defaultPipeline but is package-local
// so compresscmd stays dependency-compatible with the rest of
// cmd/thlibo without exposing execcmd internals.
func buildPipeline() (*middleware.Pipeline, error) {
	userDir := os.Getenv("THLIBO_PROCESSORS_DIR")
	if userDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userDir = home + string(os.PathSeparator) + ".thlibo" +
				string(os.PathSeparator) + "processors"
		}
	}

	reg, _, err := middleware.BuildRegistry(userDir)
	if err != nil {
		return nil, err
	}

	client := &router.DaemonClient{
		Address: ipc.DefaultInferenceAddress(),
	}
	promptRunner := &middleware.PromptRunner{Client: client}

	return &middleware.Pipeline{
		Registry:   reg,
		Router:     &router.ClientAdapter{Client: client},
		Dispatcher: &processors.Dispatcher{PromptClient: promptRunner},
	}, nil
}
