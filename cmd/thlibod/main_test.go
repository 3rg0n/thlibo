package main

import (
	"strings"
	"testing"
)

// #13: buildEngineArgs must push -c <ctx> and each --stop <token> into the
// engine argv, ahead of any operator-supplied --engine-args, so the
// llamafile spawn always has a bounded context and turn-boundary
// stops even when the operator doesn't specify them.
func TestBuildConfigEmitsCtxAndStopArgs(t *testing.T) {
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
	if !strings.Contains(joined, "--stop <turn|>") {
		t.Errorf("argv missing `--stop <turn|>`: %q", joined)
	}
	if !strings.Contains(joined, "--stop <end_of_turn>") {
		t.Errorf("argv missing `--stop <end_of_turn>`: %q", joined)
	}
}

// #13: operator-supplied --engine-args must appear AFTER the built-in
// -c / --stop args so last-value-wins (llamafile honours the trailing
// flag on conflict) favours the operator's override.
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

// #13: empty -stop and non-positive -ctx suppress the respective
// flag family, so an operator who explicitly wants the llamafile
// default can set them to empty/zero.
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
	if strings.Contains(joined, "--stop") {
		t.Errorf("empty stopTokens should suppress --stop; got %q", joined)
	}
}
