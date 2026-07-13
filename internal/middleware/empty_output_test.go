package middleware

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/processors"
)

// TestFastPathEmptyOutputFallsOpen: a processor that fast-path-matches,
// exits 0, but writes NOTHING must not blank the tool output — the
// pipeline falls open to the original input (never-break-the-client,
// ADR 0006). Original contribution by @rbrundav-gh (PR #36).
func TestFastPathEmptyOutputFallsOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script processor test needs a POSIX shell")
	}
	bash := requireBashForEmpty(t)
	_ = bash

	reg := emptyOutputFastPathProcessor(t, "blanker")
	p := newPipeline(reg, &fakeRouter{})

	input := bigInput() // > MinBytesForRouting, and matches the .* regex
	var out bytes.Buffer
	if err := p.Process(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Process returned error %v; must be nil on fallback", err)
	}
	if out.String() != input {
		t.Errorf("empty-output processor blanked the tool output; want verbatim passthrough.\n got %d bytes, want %d", out.Len(), len(input))
	}
}

// emptyOutputFastPathProcessor builds a script processor with a match
// regex (so it fast-paths) whose entry prints nothing and exits 0.
func emptyOutputFastPathProcessor(t *testing.T, name string) *processors.Registry {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	// match ".*" so any input fast-path-hits this processor; the script
	// drains stdin and prints nothing (exit 0 = success, empty output).
	if err := os.WriteFile(filepath.Join(d, "processor.yaml"),
		[]byte("name: "+name+"\ntype: script\nentry: run.sh\nmatch: \".*\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "run.sh"),
		[]byte("#!/usr/bin/env bash\ncat >/dev/null\n"), 0o755); err != nil { // #nosec G306
		t.Fatal(err)
	}
	r, _, err := processors.BuildFromDisk("", dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func requireBashForEmpty(t *testing.T) string {
	t.Helper()
	// mirrors the other script-dispatch tests' shell requirement.
	return "bash"
}
