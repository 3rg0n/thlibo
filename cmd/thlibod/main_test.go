package main

import (
	"strings"
	"testing"
)

// #13: buildEngineArgs must push -c <ctx> into the engine argv ahead of
// any operator-supplied --engine-args, so llamafile always has a bounded
// context window.
// Note: --stop tokens are no longer CLI args; they are per-request API body
// fields (see issue #7 fix). buildEngineArgs deliberately excludes them.
func TestBuildConfigEmitsCtxArgs(t *testing.T) {
	f := &flags{
		enginePath: "/usr/local/bin/thlibo-engine",
		ctxSize:    defaultCtxSize,
		stopTokens: defaultStopTokens,
	}
	args := buildEngineArgs(f)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-c 32768") {
		t.Errorf("argv missing `-c 32768`: %q", joined)
	}
	// --stop must NOT appear in engine CLI args (issue #7: server mode
	// rejects --stop; stop tokens go in the API request body instead).
	if strings.Contains(joined, "--stop") {
		t.Errorf("argv must not contain --stop (issue #7): %q", joined)
	}
}

// #13: operator-supplied --engine-args must appear AFTER the built-in
// -c arg so last-value-wins (llamafile honours the trailing flag on
// conflict) favours the operator's override.
func TestBuildConfigOperatorArgsComeLast(t *testing.T) {
	f := &flags{
		enginePath: "/usr/local/bin/thlibo-engine",
		ctxSize:    defaultCtxSize,
		stopTokens: defaultStopTokens,
		engineArgs: "-c 8192 --ngl 99",
	}
	args := buildEngineArgs(f)
	// Find indices of the first -c (ours) and the second -c
	// (operator's). Operator's should come strictly after ours.
	first, second := -1, -1
	for i, a := range args {
		if a == "-c" {
			if first < 0 {
				first = i
				continue
			}
			second = i
			break
		}
	}
	if first < 0 || second < 0 {
		t.Fatalf("expected two -c entries; got args=%v", args)
	}
	if !(first < second) {
		t.Errorf("operator -c must appear after built-in -c; got first=%d second=%d", first, second)
	}
}

// #13: non-positive -ctx suppresses the -c flag so an operator who
// explicitly wants the llamafile default can set it to zero.
func TestBuildConfigZeroValuesSuppress(t *testing.T) {
	f := &flags{
		enginePath: "/usr/local/bin/thlibo-engine",
		ctxSize:    0,
		stopTokens: "",
	}
	joined := strings.Join(buildEngineArgs(f), " ")
	if strings.Contains(joined, "-c ") {
		t.Errorf("ctxSize=0 should suppress -c; got %q", joined)
	}
}
