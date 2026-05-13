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
	if strings.Contains(s, "rtk rewrite") {
		t.Error("Codex hook still references rtk")
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
// with just the feature-flag section.
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
	if !strings.Contains(s, "codex_hooks = true") {
		t.Errorf("output missing codex_hooks: %q", s)
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
	if !strings.Contains(s, "codex_hooks = true") {
		t.Errorf("codex_hooks not added: %q", s)
	}
}

// TestEnableHooksFeatureFlagAddsKeyToExistingFeaturesSection: if
// [features] exists but codex_hooks isn't in it, we add it without
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
	if !strings.Contains(s, "codex_hooks = true") {
		t.Errorf("codex_hooks missing: %q", s)
	}
	if !strings.Contains(s, "some_other_feature = true") {
		t.Errorf("other feature lost: %q", s)
	}
}

// TestEnableHooksFeatureFlagIdempotent: 3 runs produce one
// codex_hooks entry.
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
	if strings.Count(s, "codex_hooks") != 1 {
		t.Errorf("expected 1 codex_hooks line, got %d: %q",
			strings.Count(s, "codex_hooks"), s)
	}
}

// TestEnableHooksFeatureFlagOverwritesWrongValue: a user who set
// codex_hooks = false gets it flipped to true (we own this flag;
// without it our hooks don't run).
func TestEnableHooksFeatureFlagOverwritesWrongValue(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	existing := `[features]
codex_hooks = false
`
	_ = os.WriteFile(cfg, []byte(existing), 0o600)
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	if !strings.Contains(s, "codex_hooks = true") {
		t.Errorf("flag not flipped to true: %q", s)
	}
	if strings.Contains(s, "codex_hooks = false") {
		t.Errorf("stale false value still present: %q", s)
	}
}
