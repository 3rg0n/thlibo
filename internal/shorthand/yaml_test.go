package shorthand

import (
	"context"
	"strings"
	"testing"
)

// echoBackend reflects input back unchanged. Used to test the YAML
// walker without invoking the eval gate (since input==output is
// always Safe).
type echoBackend struct{}

func (echoBackend) Run(_ context.Context, in string) (string, error) { return in, nil }

// loweringBackend lowercases the first word — a reversible-ish but
// detectable transform that lets us tell which scalars were
// rewritten and which weren't. Used to confirm the walker only
// touches prose scalars, not keys / lists / structural fields.
type loweringBackend struct{}

func (loweringBackend) Run(_ context.Context, in string) (string, error) {
	if i := strings.Index(in, " "); i > 0 {
		return strings.ToLower(in[:i]) + in[i:], nil
	}
	return strings.ToLower(in), nil
}

func TestIsYAMLContent(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"---\nname: x\n", true},
		{"name: x\n", true},
		{"name:\n  child: y\n", true},
		{"# just a comment\nname: x\n", true},
		{"# This is markdown\n# not yaml.\n", false},
		{"Hello world. This is prose.\n", false},
		{"```python\nprint('hi')\n```\n", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsYAMLContent(tc.s); got != tc.want {
			t.Errorf("IsYAMLContent(%q) = %v, want %v", firstLine(tc.s), got, tc.want)
		}
	}
}

// Walker must hit every value-position scalar but NOT keys.
func TestWalkYAMLVisitsScalarsOnly(t *testing.T) {
	// Block-scalar style is enough to qualify regardless of total
	// length, but we use a long body so even after the >=80 floor
	// passes there's no doubt.
	in := `name: x
description: |
  A long block scalar with multiple lines of content
  that should comfortably exceed the eighty-character
  floor for block-scalar prose detection.
allowed_tools:
  - Bash
  - Edit
nested:
  inner: short
  long_text: a long sentence with several words and meaningful content that goes on for plenty of characters far beyond the threshold needed`

	e := &Engine{Backend: echoBackend{}}
	r, err := e.CompressYAML(context.Background(), in)
	if err != nil {
		t.Fatalf("CompressYAML: %v", err)
	}
	// Echo backend = no real changes, every eligible scalar is
	// "rewritten" to its own value (which is fine — eval passes).
	// Result should not be marked AlreadyShorthand: the walker
	// found candidates.
	if r.AlreadyShorthand {
		t.Error("expected at least one prose scalar to be visited; got AlreadyShorthand=true")
	}
}

// allowed_tools is a structural list; even with long string values
// it must NEVER be rewritten because those are tool identifiers.
func TestCompressYAMLPreservesAllowedTools(t *testing.T) {
	in := `name: code-review
allowed_tools:
  - Bash(git diff *)
  - Bash(git log *)
  - Read
description: this is a long description about reviewing pull requests with care and rigor`

	e := &Engine{Backend: loweringBackend{}}
	r, err := e.CompressYAML(context.Background(), in)
	if err != nil {
		t.Fatalf("CompressYAML: %v", err)
	}
	if !r.Safe() {
		t.Logf("eval failures: %v", r.EvalFailures)
	}
	// The allowed_tools entries must survive byte-identical even
	// though the lowering backend would have changed them.
	for _, must := range []string{
		"Bash(git diff *)", "Bash(git log *)", "Read",
	} {
		if !strings.Contains(r.Compressed, must) {
			t.Errorf("allowed_tools entry %q missing from compressed output:\n%s", must, r.Compressed)
		}
	}
}

// Block-scalar prose values DO get rewritten when the eval passes.
func TestCompressYAMLRewritesLongDescription(t *testing.T) {
	in := `name: x
description: This is a really really long description that should be eligible for shorthand because it contains plenty of natural prose words and far exceeds the threshold for plain-scalar detection.
`
	// Keep the lowering backend — it produces a detectable change
	// while preserving every word so the eval passes.
	e := &Engine{Backend: loweringBackend{}}
	r, err := e.CompressYAML(context.Background(), in)
	if err != nil {
		t.Fatalf("CompressYAML: %v", err)
	}
	if !r.Safe() {
		t.Fatalf("expected Safe()=true; failures=%v", r.EvalFailures)
	}
	// `name: x` must survive (short scalar, blocklisted key).
	if !strings.Contains(r.Compressed, "name: x") {
		t.Errorf("name field clobbered:\n%s", r.Compressed)
	}
	// Description should differ from original (lowered first word).
	if strings.Contains(r.Compressed, "This is a really") {
		t.Errorf("expected description rewritten; got verbatim copy:\n%s", r.Compressed)
	}
}

// Round-trip: a YAML document with no eligible prose scalars (only
// short identifiers) should be reported AlreadyShorthand and emit
// the original bytes.
func TestCompressYAMLNoCandidatesIsAlreadyShorthand(t *testing.T) {
	in := `name: x
version: 1
type: prompt
match: '\bERROR\b'
`
	e := &Engine{Backend: loweringBackend{}}
	r, err := e.CompressYAML(context.Background(), in)
	if err != nil {
		t.Fatalf("CompressYAML: %v", err)
	}
	if !r.AlreadyShorthand {
		t.Error("expected AlreadyShorthand=true when no prose candidates exist")
	}
	if r.Compressed != in {
		t.Errorf("AlreadyShorthand should leave content unchanged:\nwant: %q\ngot:  %q", in, r.Compressed)
	}
}

// Eval-failure on one scalar must not poison the whole doc — the
// failing scalar reverts but other safe rewrites survive.
type droppingDirectiveBackend struct{}

func (droppingDirectiveBackend) Run(_ context.Context, in string) (string, error) {
	// Strips "MUST" and "NEVER" from the input — guaranteed to
	// trip the eval gate.
	out := strings.ReplaceAll(in, "MUST", "should")
	out = strings.ReplaceAll(out, "NEVER", "do not")
	return out, nil
}

func TestCompressYAMLRevertsScalarThatFailsEval(t *testing.T) {
	in := `description: This is long enough prose that includes a MUST directive that is load-bearing.
nested:
  text: another long sentence that does not include any directives but is plenty of words long for prose detection
`
	e := &Engine{Backend: droppingDirectiveBackend{}}
	r, err := e.CompressYAML(context.Background(), in)
	if err != nil {
		t.Fatalf("CompressYAML: %v", err)
	}
	// The MUST directive must survive — the failing scalar
	// reverted to its original value.
	if !strings.Contains(r.Compressed, "MUST directive") {
		t.Errorf("MUST directive scalar should have reverted:\n%s", r.Compressed)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
