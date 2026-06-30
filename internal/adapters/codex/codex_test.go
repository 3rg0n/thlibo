package codex

import (
	"encoding/json"
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

// TestMergeHooksJSONFreshFile: the written file matches the Codex
// schema (hooks wrapped in a top-level "hooks" object).
func TestMergeHooksJSONFreshFile(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	cmd := filepath.Join(dir, "thlibo-rewrite-codex.sh")

	if err := MergeHooksJSON(hp, cmd); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var got map[string]any
	raw, _ := os.ReadFile(hp)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}

	hooks, ok := got["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("top-level hooks key missing; raw: %s", raw)
	}
	post, _ := hooks["PostToolUse"].([]any)
	if len(post) != 1 {
		t.Fatalf("PostToolUse groups = %d, want 1: %s", len(post), raw)
	}
	group := post[0].(map[string]any)
	if group["matcher"] != "^Bash$" {
		t.Errorf("matcher = %v, want ^Bash$", group["matcher"])
	}
	entry := group["hooks"].([]any)[0].(map[string]any)
	if entry["type"] != "command" {
		t.Errorf("type = %v", entry["type"])
	}
	wantCmd := strings.ReplaceAll(cmd, `\`, "/")
	if entry["command"] != wantCmd {
		t.Errorf("command = %v, want %v", entry["command"], wantCmd)
	}
}

// TestMergeHooksJSONPreservesOtherKeys: unrelated events and keys
// survive the merge.
func TestMergeHooksJSONPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	cmd := filepath.Join(dir, "thlibo-rewrite-codex.sh")

	existing := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo session"},
					},
				},
			},
			"PostToolUse": []any{
				map[string]any{
					"matcher": "^Bash$",
					"hooks": []any{
						map[string]any{"type": "command", "command": "my-other-observer.sh"},
					},
				},
			},
		},
	}
	raw, _ := json.MarshalIndent(existing, "", "  ")
	_ = os.WriteFile(hp, raw, 0o600)

	if err := MergeHooksJSON(hp, cmd); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var got map[string]any
	out, _ := os.ReadFile(hp)
	_ = json.Unmarshal(out, &got)

	hooks := got["hooks"].(map[string]any)
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("SessionStart event lost")
	}
	// PostToolUse ^Bash$ group should now have TWO entries: the
	// pre-existing observer + our thlibo hook.
	post := hooks["PostToolUse"].([]any)
	if len(post) != 1 {
		t.Fatalf("expected 1 matcher group (both entries under ^Bash$), got %d", len(post))
	}
	entries := post[0].(map[string]any)["hooks"].([]any)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in ^Bash$ group (observer+thlibo), got %d", len(entries))
	}
}

// TestMergeHooksJSONIdempotent: multiple runs leave exactly one thlibo entry.
func TestMergeHooksJSONIdempotent(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	cmd := filepath.Join(dir, "thlibo-rewrite-codex.sh")

	for i := 0; i < 4; i++ {
		if err := MergeHooksJSON(hp, cmd); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	var got map[string]any
	raw, _ := os.ReadFile(hp)
	_ = json.Unmarshal(raw, &got)
	post := got["hooks"].(map[string]any)["PostToolUse"].([]any)
	if len(post) != 1 {
		t.Fatalf("want 1 matcher group after 4 installs, got %d", len(post))
	}
	entries := post[0].(map[string]any)["hooks"].([]any)
	if len(entries) != 1 {
		t.Errorf("want 1 thlibo entry after 4 installs, got %d", len(entries))
	}
}

// TestMergeHooksJSONRejectsInvalidJSON: we never overwrite corrupt
// JSON; a bad file stays bad and we return an error.
func TestMergeHooksJSONRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	original := []byte("{not json at all")
	_ = os.WriteFile(hp, original, 0o600)

	if err := MergeHooksJSON(hp, "/x/hook.sh"); err == nil {
		t.Fatal("expected parse error")
	}
	got, _ := os.ReadFile(hp)
	if !reflect.DeepEqual(got, original) {
		t.Errorf("corrupt file was modified: %q", got)
	}
}

// TestMergeHooksJSONNormalisesBackslashes: Windows path safety.
func TestMergeHooksJSONNormalisesBackslashes(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	winPath := `C:\Users\me\.thlibo\hooks\thlibo-rewrite-codex.sh`
	if err := MergeHooksJSON(hp, winPath); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var got map[string]any
	raw, _ := os.ReadFile(hp)
	_ = json.Unmarshal(raw, &got)
	entry := got["hooks"].(map[string]any)["PostToolUse"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	cmd := entry["command"].(string)
	if strings.Contains(cmd, `\`) {
		t.Errorf("command not normalised: %q", cmd)
	}
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
