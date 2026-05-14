package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// D1: the embedded hook script includes the expected structural
// elements. If someone edits hook.sh in a breaking way, this fails.
func TestEmbeddedHookShape(t *testing.T) {
	s := string(HookScript())
	must := []string{
		"#!/usr/bin/env bash",
		"thlibo-hook-version:",
		"tool_input.command",
		"thlibo rewrite",
		"hookSpecificOutput",
		"updatedInput",
		`"permissionDecision"`,
		`"permissionDecisionReason"`,
	}
	for _, m := range must {
		if !strings.Contains(s, m) {
			t.Errorf("hook script missing %q", m)
		}
	}
	// Guard against the hook being copy-pasted without updating
	// the rewrite command name.
	// Guard against the hook accidentally calling a different
	// rewrite binary (history: early drafts referenced another
	// project's CLI; regression check catches re-introductions).
	for _, forbidden := range []string{"rtk rewrite", "rewrite.sh rewrite"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("hook script contains forbidden reference %q; must call `thlibo rewrite`", forbidden)
		}
	}
}

// WriteHookScript creates the file with the expected contents and
// (on Unix) an executable mode.
func TestWriteHookScript(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "sub", "thlibo-rewrite.sh")
	if err := WriteHookScript(dest); err != nil {
		t.Fatalf("WriteHookScript: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read written hook: %v", err)
	}
	if !reflect.DeepEqual(got, HookScript()) {
		t.Errorf("written contents differ from embedded hook script")
	}
}

// v0.2 / #12: MergeSettingsFull registers both Bash and PowerShell
// matchers when both hook paths are provided, and uses a powershell
// -File invocation for the PowerShell entry so execution-policy
// lockdowns don't block us.
func TestMergeSettingsFullBashAndPS1(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bashHp := filepath.Join(dir, "thlibo-rewrite.sh")
	ps1Hp := filepath.Join(dir, "thlibo-rewrite.ps1")

	if err := MergeSettingsFull(sp, bashHp, ps1Hp); err != nil {
		t.Fatalf("MergeSettingsFull: %v", err)
	}

	var got map[string]any
	raw, _ := os.ReadFile(sp)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse written settings: %v\n%s", err, raw)
	}

	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse groups = %d, want 2 (Bash+PowerShell): %s", len(pre), raw)
	}
	matchers := map[string]map[string]any{}
	for _, g := range pre {
		obj := g.(map[string]any)
		matchers[obj["matcher"].(string)] = obj
	}
	if matchers["Bash"] == nil {
		t.Fatal("Bash matcher missing")
	}
	if matchers["PowerShell"] == nil {
		t.Fatal("PowerShell matcher missing")
	}

	ps1Entry := matchers["PowerShell"]["hooks"].([]any)[0].(map[string]any)
	cmd := ps1Entry["command"].(string)
	if !strings.Contains(cmd, "powershell") {
		t.Errorf("PowerShell command should invoke powershell, got %q", cmd)
	}
	if !strings.Contains(cmd, "-ExecutionPolicy Bypass") {
		t.Errorf("PowerShell command should set ExecutionPolicy Bypass, got %q", cmd)
	}
	if !strings.Contains(cmd, "-File") {
		t.Errorf("PowerShell command should use -File invocation, got %q", cmd)
	}
	if !strings.Contains(cmd, ".ps1") {
		t.Errorf("PowerShell command should reference the .ps1 hook, got %q", cmd)
	}
}

// v0.2 / #12: MergeSettingsFull with an empty ps1 path is the legacy
// "Bash only" behaviour; MergeSettings (deprecated) delegates to this.
func TestMergeSettingsFullBashOnly(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bashHp := filepath.Join(dir, "thlibo-rewrite.sh")

	if err := MergeSettingsFull(sp, bashHp, ""); err != nil {
		t.Fatalf("MergeSettingsFull: %v", err)
	}

	raw, _ := os.ReadFile(sp)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("PreToolUse groups = %d, want 1 (Bash only)", len(pre))
	}
	if pre[0].(map[string]any)["matcher"] != "Bash" {
		t.Error("expected only Bash matcher")
	}
}

// v0.2 / #12: running MergeSettingsFull twice does not duplicate the
// PowerShell entry.
func TestMergeSettingsFullIdempotent(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bashHp := filepath.Join(dir, "thlibo-rewrite.sh")
	ps1Hp := filepath.Join(dir, "thlibo-rewrite.ps1")

	for i := 0; i < 2; i++ {
		if err := MergeSettingsFull(sp, bashHp, ps1Hp); err != nil {
			t.Fatalf("MergeSettingsFull pass %d: %v", i, err)
		}
	}
	raw, _ := os.ReadFile(sp)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse groups = %d, want exactly 2 after two merges", len(pre))
	}
	for _, g := range pre {
		hooks := g.(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Errorf("%v: entries = %d, want 1 (duplicate!)", g, len(hooks))
		}
	}
}

// v0.2 / #12: WriteHookScriptPS1 writes the PowerShell hook bytes
// with an executable bit.
func TestWriteHookScriptPS1(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "thlibo-rewrite.ps1")
	if err := WriteHookScriptPS1(dest); err != nil {
		t.Fatalf("WriteHookScriptPS1: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !reflect.DeepEqual(got, HookScriptPS1()) {
		t.Errorf("written PS1 differs from embedded")
	}
	if !strings.Contains(string(got), "PowerShell") {
		t.Errorf("embedded PS1 missing PowerShell keyword; wrong file?")
	}
}

// D1 / E4: merging into a non-existent settings file creates it with
// the right nested structure.
func TestMergeSettingsFreshFile(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	hp := filepath.Join(dir, "thlibo-rewrite.sh")

	if err := MergeSettings(sp, hp); err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]any
	raw, _ := os.ReadFile(sp)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse written settings: %v\n%s", err, raw)
	}

	hooks, _ := got["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("PreToolUse groups = %d, want 1: %s", len(pre), raw)
	}
	group := pre[0].(map[string]any)
	if group["matcher"] != "Bash" {
		t.Errorf("matcher = %v, want Bash", group["matcher"])
	}
	entries := group["hooks"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0].(map[string]any)
	if entry["type"] != "command" {
		t.Errorf("type = %v, want command", entry["type"])
	}
	// Paths are stored with forward slashes so bash -c can execute
	// them on Windows; compare after normalising the expected path.
	wantCmd := strings.ReplaceAll(hp, `\`, "/")
	if entry["command"] != wantCmd {
		t.Errorf("command = %v, want %v", entry["command"], wantCmd)
	}
}

// E4: existing settings with unrelated hooks and top-level keys
// survive unchanged. Our entry gets appended into the right spot.
func TestMergeSettingsPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	hp := filepath.Join(dir, "thlibo-rewrite.sh")

	existing := map[string]any{
		"model": "claude-sonnet-4-6",
		"permissions": map[string]any{
			"defaultMode": "acceptEdits",
		},
		"hooks": map[string]any{
			"Notification": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": "notify-send"},
					},
				},
			},
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Edit|Write",
					"hooks": []any{
						map[string]any{"type": "command", "command": "prettier --write"},
					},
				},
			},
		},
	}
	raw, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(sp, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MergeSettings(sp, hp); err != nil {
		t.Fatalf("MergeSettings: %v", err)
	}

	var got map[string]any
	rawOut, _ := os.ReadFile(sp)
	_ = json.Unmarshal(rawOut, &got)

	// Top-level keys survive.
	if got["model"] != "claude-sonnet-4-6" {
		t.Errorf("model setting lost: %v", got["model"])
	}
	perms := got["permissions"].(map[string]any)
	if perms["defaultMode"] != "acceptEdits" {
		t.Errorf("permissions.defaultMode lost: %v", perms["defaultMode"])
	}

	// Notification hook survives.
	hooks := got["hooks"].(map[string]any)
	if _, ok := hooks["Notification"].([]any); !ok {
		t.Error("Notification hook removed by merge")
	}

	// PreToolUse now has 2 matcher groups: Edit|Write (existing)
	// and Bash (ours).
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse groups = %d, want 2 (existing + new):\n%s",
			len(pre), rawOut)
	}
	var sawEditWrite, sawBash bool
	for _, g := range pre {
		obj := g.(map[string]any)
		switch obj["matcher"] {
		case "Edit|Write":
			sawEditWrite = true
		case "Bash":
			sawBash = true
		}
	}
	if !sawEditWrite {
		t.Error("Edit|Write matcher group lost")
	}
	if !sawBash {
		t.Error("Bash matcher group not added")
	}
}

// E4: idempotency. Running MergeSettings twice doesn't create a
// second thlibo entry.
func TestMergeSettingsIdempotent(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	hp := filepath.Join(dir, "thlibo-rewrite.sh")

	for i := 0; i < 3; i++ {
		if err := MergeSettings(sp, hp); err != nil {
			t.Fatalf("MergeSettings pass %d: %v", i, err)
		}
	}

	var got map[string]any
	raw, _ := os.ReadFile(sp)
	_ = json.Unmarshal(raw, &got)
	hooks := got["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("expected 1 matcher group after 3 installs, got %d", len(pre))
	}
	entries := pre[0].(map[string]any)["hooks"].([]any)
	if len(entries) != 1 {
		t.Errorf("expected 1 hook entry after 3 installs, got %d", len(entries))
	}
}

// E4: a path change (user moved the script) updates the existing
// entry instead of adding a new one. Recognised by the hookMarker.
func TestMergeSettingsUpdatesOnPathChange(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	firstHook := filepath.Join(dir, "old", "thlibo-rewrite.sh")
	secondHook := filepath.Join(dir, "new", "thlibo-rewrite.sh")

	if err := MergeSettings(sp, firstHook); err != nil {
		t.Fatal(err)
	}
	if err := MergeSettings(sp, secondHook); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	raw, _ := os.ReadFile(sp)
	_ = json.Unmarshal(raw, &got)
	hooks := got["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	group := pre[0].(map[string]any)
	entries := group["hooks"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after path change, got %d", len(entries))
	}
	entry := entries[0].(map[string]any)
	wantCmd := strings.ReplaceAll(secondHook, `\`, "/")
	if entry["command"] != wantCmd {
		t.Errorf("entry.command = %v, want updated path %v", entry["command"], wantCmd)
	}
}

// Windows paths with backslashes must be normalised to forward
// slashes before being written to settings.json, because Claude
// Code spawns hooks via `bash -c "<command>"` and bash eats
// backslashes. Caught by a real `claude -p` smoke test before
// anyone else would have hit it in the wild.
func TestMergeSettingsNormalisesBackslashPath(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	// Simulate a Windows-style path going in.
	winPath := `C:\dev\Github\thlibo\.test\hooks\thlibo-rewrite.sh`
	if err := MergeSettings(sp, winPath); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	raw, _ := os.ReadFile(sp)
	_ = json.Unmarshal(raw, &got)
	hooks := got["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	entry := pre[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	cmd, _ := entry["command"].(string)
	if strings.Contains(cmd, `\`) {
		t.Errorf("command still has backslashes: %q", cmd)
	}
	wantSuffix := ".test/hooks/thlibo-rewrite.sh"
	if !strings.HasSuffix(cmd, wantSuffix) {
		t.Errorf("command = %q, want suffix %q", cmd, wantSuffix)
	}
}

// A legacy entry written with backslashes by an older version must
// get upgraded in place, not duplicated, on the next install pass.
func TestMergeSettingsUpgradesLegacyBackslashEntry(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	// Pre-seed with a legacy-style entry.
	legacy := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": `C:\old\path\thlibo-rewrite.sh`,
						},
					},
				},
			},
		},
	}
	raw, _ := json.MarshalIndent(legacy, "", "  ")
	_ = os.WriteFile(sp, raw, 0o600)

	newPath := `C:\new\path\thlibo-rewrite.sh`
	if err := MergeSettings(sp, newPath); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	rawOut, _ := os.ReadFile(sp)
	_ = json.Unmarshal(rawOut, &got)
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	entries := pre[0].(map[string]any)["hooks"].([]any)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry after upgrade, got %d", len(entries))
	}
	cmd := entries[0].(map[string]any)["command"].(string)
	if strings.Contains(cmd, `\`) {
		t.Errorf("upgraded command still has backslashes: %q", cmd)
	}
	if !strings.HasSuffix(cmd, "/new/path/thlibo-rewrite.sh") {
		t.Errorf("upgraded command = %q", cmd)
	}
}

// MergeSettings rejects invalid JSON instead of silently overwriting.
func TestMergeSettingsRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(sp, []byte("{not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := MergeSettings(sp, "/x/thlibo-rewrite.sh")
	if err == nil {
		t.Fatal("expected error on invalid JSON input")
	}
	// The original corrupt file should remain untouched — we never
	// want to clobber a user's settings on a parse failure.
	raw, _ := os.ReadFile(sp)
	if string(raw) != "{not json at all" {
		t.Errorf("settings file was modified despite parse error: %q", raw)
	}
}
