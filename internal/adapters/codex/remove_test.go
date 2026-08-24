package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config.toml in the shape a real machine holds it: another tool's
// inline hook, the feature flag, Codex's own trust record for thlibo's
// script, and thlibo's block appended last.
const mixedConfigTOML = `model = "gpt-5"

[features]
hooks = true

[[hooks.PostToolUse]]
matcher = "^Bash$"

[[hooks.PostToolUse.hooks]]
type = "command"
command = '/home/u/.local/bin/git-ai-hook'

[hooks.state.'/home/u/.thlibo/hooks/thlibo-rewrite-codex.sh:post_tool_use:1:0']
trusted = true

[[hooks.PostToolUse]]
matcher = "^Bash$"

[[hooks.PostToolUse.hooks]]
type = "command"
command = '/home/u/.thlibo/hooks/thlibo-rewrite-codex.sh'
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readConfig(t *testing.T, p string) string {
	t.Helper()
	buf, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

// TestRemoveConfigTOMLHook_MixedLayer is the #137 case. thlibo's whole
// two-table block goes, and so does the trust record naming the script we
// are about to delete. Everything else — the other tool's hook, the
// feature flag, unrelated keys — survives.
func TestRemoveConfigTOMLHook_MixedLayer(t *testing.T) {
	p := writeConfig(t, mixedConfigTOML)
	if err := RemoveConfigTOMLHook(p); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, p)

	if containsThliboMarker(got) {
		t.Errorf("config.toml still names a thlibo hook script:\n%s", got)
	}
	for _, want := range []string{
		`model = "gpt-5"`,
		"[features]",
		"hooks = true", // never cleared: other tools' hooks need it
		"command = '/home/u/.local/bin/git-ai-hook'",
		"[[hooks.PostToolUse]]", // git-ai's group must survive
	} {
		if !strings.Contains(got, want) {
			t.Errorf("removal dropped %q:\n%s", want, got)
		}
	}
	// One group left, not two: thlibo's matcher-only half must go with its
	// command half, or Codex is left a matcher with nothing to run.
	if n := strings.Count(got, "[[hooks.PostToolUse]]"); n != 1 {
		t.Errorf("[[hooks.PostToolUse]] count = %d, want 1:\n%s", n, got)
	}
	if strings.Contains(got, "hooks.state") {
		t.Errorf("trust record for the deleted script survived:\n%s", got)
	}
}

// TestRemoveConfigTOMLHook_SharedGroup: when thlibo's command sits in a
// group beside another tool's, only thlibo's [[hooks.PostToolUse.hooks]]
// table goes. The group still has a hook, so it stays.
func TestRemoveConfigTOMLHook_SharedGroup(t *testing.T) {
	p := writeConfig(t, `[[hooks.PostToolUse]]
matcher = "^Bash$"

[[hooks.PostToolUse.hooks]]
type = "command"
command = '/h/.thlibo/hooks/thlibo-rewrite-codex.sh'

[[hooks.PostToolUse.hooks]]
type = "command"
command = '/h/other-tool'
`)
	if err := RemoveConfigTOMLHook(p); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, p)

	if containsThliboMarker(got) {
		t.Errorf("thlibo hook survived:\n%s", got)
	}
	if !strings.Contains(got, "[[hooks.PostToolUse]]") || !strings.Contains(got, "command = '/h/other-tool'") {
		t.Errorf("shared group or the other tool's hook was dropped:\n%s", got)
	}
}

// TestRemoveConfigTOMLHook_PS1 covers the Windows registration shape: the
// command is a powershell invocation wrapping the script, so the marker is
// mid-string rather than the whole value.
func TestRemoveConfigTOMLHook_PS1(t *testing.T) {
	p := writeConfig(t, `[[hooks.PostToolUse]]
matcher = "^Bash$"

[[hooks.PostToolUse.hooks]]
type = "command"
command = 'powershell -NoProfile -ExecutionPolicy Bypass -File "C:/Users/u/.thlibo/hooks/thlibo-rewrite-codex.ps1"'
`)
	if err := RemoveConfigTOMLHook(p); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, p); strings.Contains(got, "hooks.PostToolUse") {
		t.Errorf("the .ps1 hook block survived:\n%s", got)
	}
}

// TestRemoveConfigTOMLHook_NoThliboHook: a config with no thlibo entry
// must come back byte-for-byte. We never reformat a file we don't own.
func TestRemoveConfigTOMLHook_NoThliboHook(t *testing.T) {
	const foreign = `[features]
hooks = true

[[hooks.PostToolUse]]
matcher = "^Bash$"

[[hooks.PostToolUse.hooks]]
type = "command"
command = '/h/git-ai'
`
	p := writeConfig(t, foreign)
	if err := RemoveConfigTOMLHook(p); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, p); got != foreign {
		t.Errorf("a config with no thlibo hook was rewritten:\n got: %q\nwant: %q", got, foreign)
	}
}

// TestRemoveConfigTOMLHook_Missing: no file is the desired end state, not
// an error.
func TestRemoveConfigTOMLHook_Missing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := RemoveConfigTOMLHook(p); err != nil {
		t.Fatalf("absent config.toml = %v, want nil", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("removal created %s", p)
	}
}

// TestRemoveConfigTOMLHook_CRLF: config.toml is often CRLF on Windows and
// we split on "\n". A line left holding nothing but a carriage return is
// invalid whitespace to a strict TOML parser — the splitCR trap, from the
// install side.
func TestRemoveConfigTOMLHook_CRLF(t *testing.T) {
	p := writeConfig(t, strings.ReplaceAll(mixedConfigTOML, "\n", "\r\n"))
	if err := RemoveConfigTOMLHook(p); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, p)
	for i, line := range strings.Split(got, "\n") {
		if line == "\r" {
			continue // a blank line in a CRLF file: correct
		}
		if strings.Contains(strings.TrimSuffix(line, "\r"), "\r") {
			t.Errorf("line %d has an interior carriage return: %q", i+1, line)
		}
	}
	if !strings.Contains(got, "command = '/home/u/.local/bin/git-ai-hook'\r") {
		t.Errorf("CRLF line endings were not preserved:\n%q", got)
	}
	if containsThliboMarker(got) {
		t.Errorf("thlibo hook survived a CRLF config:\n%s", got)
	}
}

// TestMergeThenRemoveIsIdentity: install-then-uninstall must leave the
// config exactly as it was found. This is the property that keeps the two
// halves in step — a change to the block MergeConfigTOMLHook writes has to
// stay removable.
func TestMergeThenRemoveIsIdentity(t *testing.T) {
	const before = `model = "gpt-5"

[features]
hooks = true

[[hooks.PostToolUse]]
matcher = "^Bash$"

[[hooks.PostToolUse.hooks]]
type = "command"
command = '/h/git-ai'
`
	p := writeConfig(t, before)
	if err := MergeConfigTOMLHook(p, filepath.Join("/h/.thlibo/hooks", HookFileName())); err != nil {
		t.Fatal(err)
	}
	if mid := readConfig(t, p); !containsThliboMarker(mid) {
		t.Fatalf("merge did not install a hook:\n%s", mid)
	}
	if err := RemoveConfigTOMLHook(p); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, p); got != before {
		t.Errorf("merge+remove is not an identity:\n got: %q\nwant: %q", got, before)
	}
}

// TestRemoveHooks_DeletesBothScripts: markers identify a hook by FILE, so
// a machine that has been through a .sh-era and a .ps1-era install can
// hold either name — both go, whatever host we run on (#128's lesson,
// applied to the removal side).
func TestRemoveHooks_DeletesBothScripts(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o750); err != nil {
		t.Fatal(err)
	}
	sh := filepath.Join(hookDir, hookMarkerSh)
	ps1 := filepath.Join(hookDir, hookMarkerPS1)
	keep := filepath.Join(hookDir, "thlibo-rewrite.sh") // Claude Code's; not ours to delete here
	for _, p := range []string{sh, ps1, keep} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte(mixedConfigTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	hooksJSON := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(hooksJSON, []byte(`{"hooks":{"PostToolUse":[
	  {"matcher":"^Bash$","hooks":[{"type":"command","command":"/h/git-ai"}]},
	  {"matcher":"^Bash$","hooks":[{"type":"command","command":"/h/.thlibo/hooks/thlibo-rewrite-codex.sh"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemoveHooks(cfg, hooksJSON, hookDir); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{sh, ps1} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived; stat err = %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("RemoveHooks deleted another adapter's script: %v", err)
	}
	if got := readConfig(t, cfg); containsThliboMarker(got) {
		t.Errorf("config.toml still names a thlibo hook:\n%s", got)
	}
	got := readConfig(t, hooksJSON)
	if containsThliboMarker(got) {
		t.Errorf("hooks.json still names a thlibo hook:\n%s", got)
	}
	if !strings.Contains(got, "/h/git-ai") {
		t.Errorf("hooks.json lost the other tool's entry:\n%s", got)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Errorf("hooks.json is no longer valid JSON: %v", err)
	}
}

// TestRemoveHooks_NoHookDir: an empty hookDir must not turn the script
// deletions into relative-path removals in the working directory.
func TestRemoveHooks_NoHookDir(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte(mixedConfigTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveHooks(cfg, filepath.Join(dir, "hooks.json"), ""); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, cfg); containsThliboMarker(got) {
		t.Errorf("config.toml still names a thlibo hook:\n%s", got)
	}
}

// TestTomlSectionName covers the header reader, including the two shapes
// that must NOT read as headers: an array element on its own line, and a
// key whose value is an array.
func TestTomlSectionName(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"[features]", "features", true},
		{"[[hooks.PostToolUse]]", "hooks.PostToolUse", true},
		{"[[hooks.PostToolUse.hooks]]\r", "hooks.PostToolUse.hooks", true},
		{"  [hooks.state.'/h/x.sh:post_tool_use:0:0']", "hooks.state.'/h/x.sh:post_tool_use:0:0'", true},
		{`command = 'x'`, "", false},
		{`args = ["a", "b"]`, "", false},
		{`  [1, 2],`, "", false},
		{"", "", false},
		{"[]", "", false},
	}
	for _, tc := range cases {
		got, ok := tomlSectionName(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("tomlSectionName(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestSplitTOMLSectionsIsLossless: rejoining every section's lines must
// reproduce the input exactly. The remover deletes whole sections, so any
// line the splitter drops or duplicates would silently corrupt a config.
func TestSplitTOMLSectionsIsLossless(t *testing.T) {
	for _, in := range []string{
		mixedConfigTOML,
		strings.ReplaceAll(mixedConfigTOML, "\n", "\r\n"),
		"",
		"no headers at all\n",
		"[a]\n[b]\n",
	} {
		var lines []string
		for _, s := range splitTOMLSections(in) {
			lines = append(lines, s.lines...)
		}
		if got := strings.Join(lines, "\n"); got != in {
			t.Errorf("split is lossy:\n got: %q\nwant: %q", got, in)
		}
	}
}
