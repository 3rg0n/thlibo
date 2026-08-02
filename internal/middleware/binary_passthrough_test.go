package middleware

import (
	"context"
	"strings"
	"testing"
)

// TestBinaryContainerSkipsRouter pins the second half of ADR 0014's cost
// argument. MatchFastPath refuses line-shape filters on binary input, so a
// container like a .zip or .tar.gz now finds no fast-path match. Falling
// through to the router would sample those bytes into a prompt and spend
// an inference round-trip to be told "none" — strictly worse than the
// local no-op it replaced, because the bytes leave the process.
//
// The assertion that matters is rt.called == 0: no round-trip at all.
func TestBinaryContainerSkipsRouter(t *testing.T) {
	// A line shape that WOULD match, to prove the skip isn't just "no
	// processor was interested".
	reg := withScriptProcessor(t, "greedy-filter", `(?m)^ok\s`)
	rt := &fakeRouter{}
	p := newPipeline(reg, rt)

	// Binary: NUL in the first 8 KiB, plus a coincidentally matching line.
	raw := "PK\x03\x04\x00\x00" + strings.Repeat("\xff", 40) +
		"\nok  \tsomething\t0.1s\n" + strings.Repeat("\x00", MinBytesForRouting)

	out, inv := p.decide(context.Background(), raw)

	if out != raw {
		t.Errorf("binary input must pass through byte-exact: got %d bytes, want %d", len(out), len(raw))
	}
	if rt.called != 0 {
		t.Errorf("router called %d times on a binary container, want 0 — those bytes must not leave the process", rt.called)
	}
	if inv.Outcome != "passthrough" {
		t.Errorf("outcome = %q, want passthrough", inv.Outcome)
	}
}

// TestTextInputStillReachesRouter is the control: the binary skip must not
// swallow ordinary text that no fast-path regex claimed.
func TestTextInputStillReachesRouter(t *testing.T) {
	reg := withScriptProcessor(t, "narrow-filter", `(?m)^never-matches-this\s`)
	rt := &fakeRouter{}
	p := newPipeline(reg, rt)

	raw := strings.Repeat("ordinary log line with no NUL bytes\n", 200)
	if len(raw) < MinBytesForRouting {
		t.Fatalf("fixture too small: %d", len(raw))
	}

	if _, _ = p.decide(context.Background(), raw); rt.called != 1 {
		t.Errorf("router called %d times on text input, want 1", rt.called)
	}
}
