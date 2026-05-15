package shorthand

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubBackend struct {
	out string
	err error
}

func (s *stubBackend) Run(_ context.Context, _ string) (string, error) {
	return s.out, s.err
}

func TestCompressHappyPath(t *testing.T) {
	in := "Please make sure to read the file first. Always verify output."
	want := "Read file first. ALWAYS verify output."

	r, err := (&Engine{Backend: &stubBackend{out: want}}).Compress(context.Background(), in)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if !r.Safe() {
		t.Errorf("expected Safe()=true; failures=%v", r.EvalFailures)
	}
	if r.Compressed != want {
		t.Errorf("compressed = %q, want %q", r.Compressed, want)
	}
	if r.ReductionPercent <= 0 {
		t.Errorf("expected positive reduction, got %.2f%%", r.ReductionPercent)
	}
}

func TestCompressAlreadyShorthand(t *testing.T) {
	in := "Read file. ALWAYS verify."
	r, err := (&Engine{Backend: &stubBackend{out: alreadyShorthandSentinel + "\n"}}).Compress(context.Background(), in)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if !r.AlreadyShorthand {
		t.Error("expected AlreadyShorthand=true")
	}
	if r.Compressed != in {
		t.Errorf("AlreadyShorthand should leave Compressed=Original; got %q", r.Compressed)
	}
	if r.ReductionPercent != 0 {
		t.Errorf("AlreadyShorthand should report 0%% reduction, got %.2f%%", r.ReductionPercent)
	}
}

func TestCompressBackendError(t *testing.T) {
	wantErr := errors.New("daemon down")
	r, err := (&Engine{Backend: &stubBackend{err: wantErr}}).Compress(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error")
	}
	// Caller can still emit Original from r.
	if r.Compressed != "anything" {
		t.Errorf("Compressed should fall back to Original on error; got %q", r.Compressed)
	}
}

func TestCompressNilBackend(t *testing.T) {
	_, err := (&Engine{}).Compress(context.Background(), "x")
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Errorf("expected ErrBackendUnavailable, got %v", err)
	}
}

// Eval-failure tests — the safety gate.

func TestEvalDirectivePreserved(t *testing.T) {
	in := "NEVER force-push. MUST review every PR."
	out := "DO NOT force-push. Review every PR." // NEVER and MUST dropped.
	failures := Evaluate(in, out)
	hasMissing := func(d string) bool {
		for _, f := range failures {
			if strings.Contains(f, "missing directive: "+d) {
				return true
			}
		}
		return false
	}
	if !hasMissing("NEVER") {
		t.Error("expected missing-directive failure for NEVER")
	}
	if !hasMissing("MUST") {
		t.Error("expected missing-directive failure for MUST")
	}
}

func TestEvalCodeFenceByteIdentity(t *testing.T) {
	in := "Run the snippet:\n```python\nprint('hi')\n```\n"
	out := "Run snippet:\n```python\nprint(hi)\n```\n" // changed inside the fence
	failures := Evaluate(in, out)
	any := false
	for _, f := range failures {
		if strings.Contains(f, "missing code fence") {
			any = true
		}
	}
	if !any {
		t.Errorf("expected missing-code-fence failure; got %v", failures)
	}
}

func TestEvalCodeFencePreserved(t *testing.T) {
	in := "Run:\n```python\nprint('hi')\n```\n"
	out := "Run:\n```python\nprint('hi')\n```\n"
	if failures := Evaluate(in, out); len(failures) != 0 {
		t.Errorf("identical fence should pass; got %v", failures)
	}
}

func TestEvalFrontmatterPreserved(t *testing.T) {
	in := `---
name: x
description: A long description that will be compressed.
allowed-tools: ["Read", "Edit"]
---

Body.
`
	out := `---
name: x
description: Short.
allowed-tools: ["Read", "Edit"]
---

Body.
`
	if failures := Evaluate(in, out); len(failures) != 0 {
		t.Errorf("safe frontmatter compression should pass; got %v", failures)
	}
}

func TestEvalFrontmatterKeyDropped(t *testing.T) {
	in := `---
name: x
description: y
allowed-tools: [Read]
---
Body.
`
	out := `---
name: x
description: y
---
Body.
` // allowed-tools dropped.
	failures := Evaluate(in, out)
	any := false
	for _, f := range failures {
		if strings.Contains(f, "frontmatter key dropped: allowed-tools") {
			any = true
		}
	}
	if !any {
		t.Errorf("expected frontmatter-key-dropped failure; got %v", failures)
	}
}

func TestEvalFrontmatterFullyDropped(t *testing.T) {
	in := "---\nname: x\n---\nBody.\n"
	out := "Body."
	failures := Evaluate(in, out)
	any := false
	for _, f := range failures {
		if strings.Contains(f, "frontmatter dropped") {
			any = true
		}
	}
	if !any {
		t.Errorf("expected frontmatter-dropped failure; got %v", failures)
	}
}

func TestEvalURLPreserved(t *testing.T) {
	in := "See https://example.com/docs for details."
	good := "See https://example.com/docs for details."
	bad := "See the linked docs for details."

	if failures := Evaluate(in, good); len(failures) != 0 {
		t.Errorf("URL preserved: %v", failures)
	}
	failures := Evaluate(in, bad)
	any := false
	for _, f := range failures {
		if strings.Contains(f, "https://example.com/docs") {
			any = true
		}
	}
	if !any {
		t.Errorf("expected missing-token failure for URL; got %v", failures)
	}
}

func TestEvalVersionPreserved(t *testing.T) {
	in := "Requires Go 1.26.3 minimum."
	bad := "Requires recent Go."
	failures := Evaluate(in, bad)
	any := false
	for _, f := range failures {
		if strings.Contains(f, "1.26.3") {
			any = true
		}
	}
	if !any {
		t.Errorf("expected missing version token; got %v", failures)
	}
}

func TestEvalNumericThresholdPreserved(t *testing.T) {
	in := "Files over 32 KiB are compressed."
	bad := "Large files are compressed."
	failures := Evaluate(in, bad)
	any := false
	for _, f := range failures {
		if strings.Contains(f, "32 KiB") {
			any = true
		}
	}
	if !any {
		t.Errorf("expected missing-threshold failure; got %v", failures)
	}
}

func TestEvalFilePathPreserved(t *testing.T) {
	in := "Edit ~/.claude/settings.json to enable."
	bad := "Edit your settings file to enable."
	failures := Evaluate(in, bad)
	any := false
	for _, f := range failures {
		if strings.Contains(f, "settings.json") {
			any = true
		}
	}
	if !any {
		t.Errorf("expected missing-path failure; got %v", failures)
	}
}

func TestEvalNoNewBacktickedClaims(t *testing.T) {
	in := "Pass `--quiet` to suppress output."
	bad := "Pass `--quiet` or `--silent` to suppress." // --silent invented
	failures := Evaluate(in, bad)
	any := false
	for _, f := range failures {
		if strings.Contains(f, "--silent") {
			any = true
		}
	}
	if !any {
		t.Errorf("expected new-claim failure for invented `--silent`; got %v", failures)
	}
}

func TestEvalIntegrationGoldenCompression(t *testing.T) {
	// Real-world prose-shape input from one of the study tasks.
	in := `# CHANGELOG skill

Maintain CHANGELOG.md in Keep a Changelog format.

## Trigger

- User says: "update changelog", "add entry", "prep release notes"
- User asks about adding entries

## Procedure

1. Read CHANGELOG.md
2. Identify the section to update
3. Add entry under [Unreleased]
4. NEVER edit released entries
5. MUST keep version number format vMAJOR.MINOR.PATCH
`
	out := `# CHANGELOG skill

Maintain CHANGELOG.md in Keep a Changelog format.

## Trigger

User says: "update changelog" | "add entry" | "prep release notes".

## Procedure

- Read CHANGELOG.md
- Identify section to update
- Add entry under [Unreleased]
- NEVER edit released entries
- MUST keep version format vMAJOR.MINOR.PATCH
`
	failures := Evaluate(in, out)
	if len(failures) != 0 {
		t.Errorf("safe golden compression should pass; got %v", failures)
	}
}
