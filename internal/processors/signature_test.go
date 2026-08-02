package processors

import (
	"strings"
	"testing"
)

// TestRootedPattern pins the signature-vs-line-shape derivation (#97).
// The flag sensitivity is the whole point: `^` alone anchors to the input,
// `(?m)^` anchors to any line, and only the former is a format signature.
func TestRootedPattern(t *testing.T) {
	cases := []struct {
		pat    string
		rooted bool
		why    string
	}{
		{`^%PDF-`, true, "pdf-to-md's real pattern: the case this fix exists for"},
		{`\A%PDF-`, true, `\A is the explicit start-of-input anchor`},
		{`(?s)^%PDF-`, true, "s flag affects . only, not ^"},
		{`(^%PDF-)`, true, "capture group around the anchor"},
		{`(?:^%PDF-)`, true, "non-capturing group around the anchor"},
		{`^a|^b`, true, "every branch rooted"},
		{`^(a|b)`, true, "anchor outside the alternation"},

		{`(?m)^%PDF-`, false, "m flag makes ^ start-of-LINE: not a signature"},
		{`%PDF-`, false, "no anchor at all"},
		{`^a|b`, false, "one branch floats, so a match can start mid-input"},
		{`.*^a`, false, "leading consumer means the match need not start at 0"},
		{`a^b`, false, "anchor not in leading position"},
		{`$`, false, "end anchor is not a start anchor"},
		{``, false, "empty pattern"},

		// The real builtin line shapes — every one of these must be
		// classified as a line shape, or the fix does nothing.
		{`(?m)^\s*=== RUN\s|^\s*--- (?:PASS|FAIL|SKIP):|^(?:ok|FAIL|\?)\s+\S+\s`, false, "go-test-filter: the pattern that stole PDFs"},
		{`(?m)^(On branch |HEAD detached|diff --git)`, false, "git-filter"},
		{`(?i)traceback|^error(:|\[E\d+\])|fatal|panic:`, false, "casefolder: prompt processor, broad substrings"},
	}
	for _, c := range cases {
		if got := rootedPattern(c.pat); got != c.rooted {
			t.Errorf("rootedPattern(%q) = %v, want %v — %s", c.pat, got, c.rooted, c.why)
		}
	}
}

// TestRootedPatternInvalidIsNotSignature: validate() rejects an
// uncompilable pattern before rootedPattern ever sees it, but the helper
// must not panic if that order ever changes.
func TestRootedPatternInvalidIsNotSignature(t *testing.T) {
	if rootedPattern(`^(unclosed`) {
		t.Error("an unparseable pattern must not be reported as a signature")
	}
}

// TestMatchIsSignatureSetAtValidate: the flag is derived at descriptor
// compile time, so it can't drift out of sync with Match.
func TestMatchIsSignatureSetAtValidate(t *testing.T) {
	sig := descriptorWithMatch(t, "sig-proc", `^%PDF-`)
	if !sig.MatchIsSignature() {
		t.Error("rooted Match should set MatchIsSignature")
	}
	shape := descriptorWithMatch(t, "shape-proc", `(?m)^ok\s`)
	if shape.MatchIsSignature() {
		t.Error("multiline Match must not be a signature")
	}
	none := descriptorWithMatch(t, "none-proc", "")
	if none.MatchIsSignature() {
		t.Error("absent Match must not be a signature")
	}
}

// TestSignatureBeatsAlphabeticallyEarlierLineShape is the #97 regression
// test. go-test-filter sorts before pdf-to-md and its line shape matches
// inside PDF bytes; the signature must win anyway.
func TestSignatureBeatsAlphabeticallyEarlierLineShape(t *testing.T) {
	r := registryOf(t,
		descriptorWithMatch(t, "go-test-filter", `(?m)^\s*=== RUN\s|^(?:ok|FAIL|\?)\s+\S+\s`),
		descriptorWithMatch(t, "pdf-to-md", `^%PDF-`),
	)
	// The exact shape of the reported repro: PDF header, then a
	// go-test-shaped line further in.
	input := "%PDF-1.5\n" + strings.Repeat("x", 100) + "\n=== RUN TestFoo\n" + strings.Repeat("y", 3000)

	got := r.MatchFastPath(input)
	if got == nil {
		t.Fatal("expected a fast-path match, got nil")
	}
	if got.Name != "pdf-to-md" {
		t.Errorf("MatchFastPath = %q, want pdf-to-md — an anchored signature must outrank a line shape that sorts earlier", got.Name)
	}
}

// TestLineShapeStillWinsOnTextInput: the fix must not disturb the ordinary
// case, where a line-shape filter is the only and correct answer.
func TestLineShapeStillWinsOnTextInput(t *testing.T) {
	r := registryOf(t,
		descriptorWithMatch(t, "go-test-filter", `(?m)^\s*=== RUN\s|^(?:ok|FAIL|\?)\s+\S+\s`),
		descriptorWithMatch(t, "pdf-to-md", `^%PDF-`),
	)
	got := r.MatchFastPath("=== RUN TestThing\n--- PASS: TestThing (0.00s)\nok  \texample/pkg\t0.1s\n")
	if got == nil || got.Name != "go-test-filter" {
		t.Fatalf("MatchFastPath = %v, want go-test-filter on real go test output", got)
	}
}

// TestBinaryInputRejectsLineShapes covers the half of #97 that rooting
// alone can't fix: an archive has no signature processor to claim it, but
// still false-matches a line shape. Verified against this repo's own
// release artifacts (.zip and .tar.gz both hit go-test-filter).
func TestBinaryInputRejectsLineShapes(t *testing.T) {
	r := registryOf(t,
		descriptorWithMatch(t, "go-test-filter", `(?m)^\s*=== RUN\s|^(?:ok|FAIL|\?)\s+\S+\s`),
	)
	// A NUL early, as any real archive has, plus a coincidental match.
	input := "PK\x03\x04\x00\x00" + strings.Repeat("\xff", 50) + "\nok  \tsomething\t0.1s\n" + strings.Repeat("\x00", 100)

	if got := r.MatchFastPath(input); got != nil {
		t.Errorf("MatchFastPath = %q on binary input, want nil — line-shape filters must not claim a container", got.Name)
	}
}

// TestBinaryInputStillAllowsSignatures: the binary guard must not block
// the processors whose entire job is binary input.
func TestBinaryInputStillAllowsSignatures(t *testing.T) {
	r := registryOf(t,
		descriptorWithMatch(t, "go-test-filter", `(?m)^(?:ok|FAIL|\?)\s+\S+\s`),
		descriptorWithMatch(t, "pdf-to-md", `^%PDF-`),
	)
	input := "%PDF-1.5\n\x00\x00binary stream\x00\nok  \tpkg\t0.1s\n"
	got := r.MatchFastPath(input)
	if got == nil || got.Name != "pdf-to-md" {
		t.Fatalf("MatchFastPath = %v, want pdf-to-md — a signature must still match a binary container", got)
	}
}

// TestBinaryLookingSniffsHeadOnly: a NUL past the sniff window doesn't
// count, so a large text log with binary junk appended still filters.
func TestBinaryLookingSniffsHeadOnly(t *testing.T) {
	if binaryLooking(strings.Repeat("a", binaryBySniffLen) + "\x00") {
		t.Error("NUL beyond the sniff window must not mark input binary")
	}
	if !binaryLooking(strings.Repeat("a", binaryBySniffLen-1) + "\x00") {
		t.Error("NUL inside the sniff window must mark input binary")
	}
	if binaryLooking("plain text\n") {
		t.Error("plain text must not be marked binary")
	}
	if binaryLooking("") {
		t.Error("empty input must not be marked binary")
	}
}

// TestScriptStillBeatsPromptWithinLineShapes preserves the pre-existing
// tier-2-over-tier-3 rule.
func TestScriptStillBeatsPromptWithinLineShapes(t *testing.T) {
	prompt := descriptorWithMatch(t, "aaa-prompt", `(?i)fatal`)
	prompt.Type = KindPrompt
	prompt.SystemPrompt = "x"
	script := descriptorWithMatch(t, "zzz-script", `(?m)^fatal`)

	r := registryOf(t, prompt, script)
	got := r.MatchFastPath("fatal: something broke\n")
	if got == nil || got.Name != "zzz-script" {
		t.Fatalf("MatchFastPath = %v, want zzz-script — script/native must outrank prompt even when it sorts later", got)
	}
}

// --- helpers ---

// descriptorWithMatch builds a script descriptor through validate() so the
// derived matchRooted flag is populated the same way the loader does it.
func descriptorWithMatch(t *testing.T, name, match string) *Descriptor {
	t.Helper()
	d := &Descriptor{
		Name:  name,
		Type:  KindScript,
		Entry: "run.sh",
		Match: match,
	}
	if err := validate(d); err != nil {
		t.Fatalf("validate(%s): %v", name, err)
	}
	return d
}

// registryOf assembles a registry directly, preserving the alphabetical
// r.order the real loader produces.
func registryOf(t *testing.T, ds ...*Descriptor) *Registry {
	t.Helper()
	r := &Registry{byName: map[string]*Descriptor{}}
	names := make([]string, 0, len(ds))
	for _, d := range ds {
		r.byName[d.Name] = d
		names = append(names, d.Name)
	}
	sortStrings(names)
	r.order = names
	return r
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
