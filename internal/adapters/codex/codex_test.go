package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestEmbeddedHookShape guards the bash script against regressions.
// The Codex hook uses PostToolUse + decision:block to REPLACE the
// tool result with a compressed version — different from Claude
// Code's PreToolUse-rewrite mechanism.
func TestEmbeddedHookShape(t *testing.T) {
	s := string(HookScript())
	must := []string{
		"#!/usr/bin/env bash",
		"thlibo compress",
		`"decision": "block"`,
		"PostToolUse",
		"tool_response",
	}
	for _, m := range must {
		if !strings.Contains(s, m) {
			t.Errorf("Codex hook missing %q", m)
		}
	}
	for _, forbidden := range []string{"rtk rewrite", "rewrite.sh rewrite"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("Codex hook contains forbidden reference %q; must call `thlibo compress`", forbidden)
		}
	}
	// Guard against an old draft of this hook that used PreToolUse:
	// Codex's PreToolUse doesn't support updatedInput, so that hook
	// would have been advisory-only. The PostToolUse approach is
	// the one that actually compresses.
	if strings.Contains(s, "permissionDecision") {
		t.Error("Codex hook must use PostToolUse/decision, not PreToolUse/permissionDecision")
	}
}

func TestWriteHookScript(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "sub", "thlibo-rewrite-codex.sh")
	if err := WriteHookScript(dest); err != nil {
		t.Fatalf("WriteHookScript: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !reflect.DeepEqual(got, HookScript()) {
		t.Error("written bytes differ from embedded script")
	}
}

// TestMergeConfigTOMLHookFreshFile: an empty/absent config.toml gets a
// well-formed inline [[hooks.PostToolUse]] block pointing at the hook.
func TestMergeConfigTOMLHookFreshFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	cmd := filepath.Join(dir, "thlibo-rewrite-codex.sh")

	if err := MergeConfigTOMLHook(cfg, cmd); err != nil {
		t.Fatalf("MergeConfigTOMLHook: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	for _, want := range []string{
		"[[hooks.PostToolUse]]",
		`matcher = "^Bash$"`,
		"[[hooks.PostToolUse.hooks]]",
		`type = "command"`,
		"thlibo-rewrite-codex.sh",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

// TestMergeConfigTOMLHookPreservesExistingInline: the exact real-world
// case #170 targets — a config.toml that already has another tool's
// inline hooks (git-ai style) + the feature flag. Our block is appended;
// nothing existing is touched.
func TestMergeConfigTOMLHookPreservesExistingInline(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	existing := `model = "gpt-5.5"

[features]
hooks = true

[[hooks.PostToolUse]]

[[hooks.PostToolUse.hooks]]
command = 'C:\Users\me\.git-ai\bin\git-ai.exe checkpoint codex --hook-input stdin'
type = "command"
`
	_ = os.WriteFile(cfg, []byte(existing), 0o600)
	cmd := filepath.Join(dir, "thlibo-rewrite-codex.sh")

	if err := MergeConfigTOMLHook(cfg, cmd); err != nil {
		t.Fatalf("MergeConfigTOMLHook: %v", err)
	}
	s := string(mustRead(t, cfg))
	// Everything that was there must still be there, verbatim.
	if !strings.Contains(s, existing) {
		t.Errorf("existing content not preserved verbatim:\n%s", s)
	}
	// git-ai's hook survives; ours is added.
	if !strings.Contains(s, "git-ai.exe checkpoint codex") {
		t.Error("git-ai inline hook was lost")
	}
	if !strings.Contains(s, "thlibo-rewrite-codex.sh") {
		t.Error("thlibo hook not appended")
	}
	// Two PostToolUse array-of-table entries now (git-ai's + ours).
	if n := strings.Count(s, "[[hooks.PostToolUse]]"); n != 2 {
		t.Errorf("expected 2 [[hooks.PostToolUse]] entries, got %d:\n%s", n, s)
	}
}

// TestMergeConfigTOMLHookIdempotent: re-running never adds a second
// thlibo block (recognised by the script marker).
func TestMergeConfigTOMLHookIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	cmd := filepath.Join(dir, "thlibo-rewrite-codex.sh")
	for i := 0; i < 4; i++ {
		if err := MergeConfigTOMLHook(cfg, cmd); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	s := string(mustRead(t, cfg))
	if n := strings.Count(s, "thlibo-rewrite-codex.sh"); n != 1 {
		t.Errorf("expected exactly 1 thlibo hook after 4 installs, got %d:\n%s", n, s)
	}
	if n := strings.Count(s, "[[hooks.PostToolUse]]"); n != 1 {
		t.Errorf("expected 1 PostToolUse block after 4 installs, got %d", n)
	}
}

// TestMergeConfigTOMLHookWindowsPath: a Windows hook path is emitted in
// a TOML single-quoted (literal) string so its backslashes survive
// unescaped and correct.
func TestMergeConfigTOMLHookWindowsPath(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	winPath := `C:\Users\me\.thlibo\hooks\thlibo-rewrite-codex.sh`
	if err := MergeConfigTOMLHook(cfg, winPath); err != nil {
		t.Fatalf("MergeConfigTOMLHook: %v", err)
	}
	s := string(mustRead(t, cfg))
	// normalisePath forward-slashes the path; the command line must
	// contain it inside a single-quoted literal.
	if !strings.Contains(s, `command = 'C:/Users/me/.thlibo/hooks/thlibo-rewrite-codex.sh'`) {
		t.Errorf("windows path not emitted as a forward-slashed literal:\n%s", s)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- EnableHooksFeatureFlag / ensureCodexHooksTrue ---

// TestEnableHooksFeatureFlagFreshFile: creates a new config.toml
// with just the feature-flag section. Writes the canonical key.
func TestEnableHooksFeatureFlagFreshFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	if !strings.Contains(s, "[features]") {
		t.Errorf("output missing [features]: %q", s)
	}
	if !strings.Contains(s, "hooks = true") {
		t.Errorf("output missing canonical hooks flag: %q", s)
	}
	// The deprecated alias must NOT be written on a fresh file.
	if strings.Contains(s, "codex_hooks") {
		t.Errorf("should write canonical hooks, not deprecated codex_hooks: %q", s)
	}
}

// TestEnableHooksFeatureFlagPreservesExistingConfig: an existing
// config.toml with other settings gets the feature-flag block
// added without touching anything else.
func TestEnableHooksFeatureFlagPreservesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")

	existing := `# My Codex config
model = "o1"

[auth]
provider = "chatgpt"
`
	_ = os.WriteFile(cfg, []byte(existing), 0o600)

	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	if !strings.Contains(s, `model = "o1"`) {
		t.Errorf("model line lost: %q", s)
	}
	if !strings.Contains(s, "[auth]") || !strings.Contains(s, `provider = "chatgpt"`) {
		t.Errorf("auth section lost: %q", s)
	}
	if !strings.Contains(s, "hooks = true") {
		t.Errorf("hooks flag not added: %q", s)
	}
}

// TestEnableHooksFeatureFlagAddsKeyToExistingFeaturesSection: if
// [features] exists but the hooks flag isn't in it, we add it without
// creating a second [features] header.
func TestEnableHooksFeatureFlagAddsKeyToExistingFeaturesSection(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")

	existing := `[features]
some_other_feature = true

[auth]
provider = "chatgpt"
`
	_ = os.WriteFile(cfg, []byte(existing), 0o600)
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	// Should be exactly one [features] section header.
	if strings.Count(s, "[features]") != 1 {
		t.Errorf("expected exactly one [features] header, got %d: %q",
			strings.Count(s, "[features]"), s)
	}
	if !strings.Contains(s, "hooks = true") {
		t.Errorf("hooks flag missing: %q", s)
	}
	if !strings.Contains(s, "some_other_feature = true") {
		t.Errorf("other feature lost: %q", s)
	}
}

// TestEnableHooksFeatureFlagIdempotent: 3 runs produce one hooks entry.
func TestEnableHooksFeatureFlagIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	for i := 0; i < 3; i++ {
		if err := EnableHooksFeatureFlag(cfg); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	// "hooks" appears in the flag line; count exact flag lines, not the
	// substring (which would also catch a [hooks] table if present).
	if n := countFlagLines(s, "hooks"); n != 1 {
		t.Errorf("expected 1 hooks flag line, got %d: %q", n, s)
	}
}

// TestEnableHooksFeatureFlagOverwritesWrongValue: a user who set
// hooks = false gets it flipped to true (we own this flag; without it
// our hooks don't run).
func TestEnableHooksFeatureFlagOverwritesWrongValue(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	existing := `[features]
hooks = false
`
	_ = os.WriteFile(cfg, []byte(existing), 0o600)
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	if !strings.Contains(s, "hooks = true") {
		t.Errorf("flag not flipped to true: %q", s)
	}
	if strings.Contains(s, "hooks = false") {
		t.Errorf("stale false value still present: %q", s)
	}
}

// TestEnableHooksFeatureFlagDeprecatedAliasSatisfies: a config that
// already has `codex_hooks = true` (the deprecated alias, e.g. from an
// older thlibo or git-ai) is left UNCHANGED — we must not add a second,
// canonical `hooks = true` line beside it (the #56-followup bug where
// the box ended up with both).
func TestEnableHooksFeatureFlagDeprecatedAliasSatisfies(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	existing := `[features]
codex_hooks = true
`
	_ = os.WriteFile(cfg, []byte(existing), 0o600)
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	if strings.Contains(s, "\nhooks = true") || strings.HasPrefix(s, "hooks = true") {
		t.Errorf("must not add canonical hooks beside existing codex_hooks alias: %q", s)
	}
	if !strings.Contains(s, "codex_hooks = true") {
		t.Errorf("existing alias should be preserved: %q", s)
	}
}

// TestEnableHooksFeatureFlagCanonicalAlreadyPresent: `hooks = true`
// already there → no change, no duplicate.
func TestEnableHooksFeatureFlagCanonicalAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	existing := `[features]
hooks = true
`
	_ = os.WriteFile(cfg, []byte(existing), 0o600)
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	if n := countFlagLines(string(got), "hooks"); n != 1 {
		t.Errorf("expected exactly 1 hooks flag line, got %d: %q", n, string(got))
	}
}

// TestEnableHooksFeatureFlagPreservesInlineComment: `hooks = true` with
// a trailing TOML comment is already enabled — must be left UNCHANGED
// (not rewritten, which would strip the comment). Review-found.
func TestEnableHooksFeatureFlagPreservesInlineComment(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	existing := "[features]\nhooks = true  # enabled by git-ai\n"
	_ = os.WriteFile(cfg, []byte(existing), 0o600)
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	if string(got) != existing {
		t.Errorf("config with `hooks = true # comment` must be untouched.\n got: %q\nwant: %q", got, existing)
	}
}

// TestEnableHooksFeatureFlagPreservesIndentationOnFlip: a `hooks = false`
// line with leading whitespace is flipped to true KEEPING its indent.
// Review-found.
func TestEnableHooksFeatureFlagPreservesIndentationOnFlip(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	existing := "[features]\n    hooks = false\n"
	_ = os.WriteFile(cfg, []byte(existing), 0o600)
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	if !strings.Contains(string(got), "    hooks = true") {
		t.Errorf("indentation not preserved on flip: %q", string(got))
	}
	if strings.Contains(string(got), "hooks = false") {
		t.Errorf("stale false value remains: %q", string(got))
	}
}

// countFlagLines counts lines that are an assignment of exactly `key`
// (so "hooks" does not match "codex_hooks" or a "[hooks]" table header).
func countFlagLines(content, key string) int {
	n := 0
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(t, key)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(rest, "=") {
			n++
		}
	}
	return n
}
