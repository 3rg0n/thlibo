package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestEmbeddedHookShape guards the bash script against regressions.
// The Cursor hook uses preToolUse + updated_input to REWRITE the Shell
// command (so its output is compressed when run) — Cursor can't
// substitute shell output the way Codex's PostToolUse decision:block
// does, so this is the command-wrap path (same as Claude Code's Bash
// hook), scoped to tool_name == "Shell".
func TestEmbeddedHookShape(t *testing.T) {
	s := string(HookScript())
	must := []string{
		"#!/usr/bin/env bash",
		"thlibo rewrite",
		"updated_input",
		`"permission"`,
		`.tool_name`,
		"Shell",
	}
	for _, m := range must {
		if !strings.Contains(s, m) {
			t.Errorf("Cursor hook missing %q", m)
		}
	}
	// Cursor shell hooks cannot substitute output — guard against a
	// draft that tried Codex's decision:block or Claude's
	// permissionDecision, neither of which Cursor understands. Check the
	// emitted-JSON forms (the jq object keys), not bare words, so the
	// explanatory comments in the script don't trip the guard.
	for _, forbidden := range []string{`"decision":`, `permissionDecision`, `"updated_mcp_tool_output":`} {
		for _, line := range strings.Split(s, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue // explanatory comment, not emitted JSON
			}
			if strings.Contains(trimmed, forbidden) {
				t.Errorf("Cursor hook uses forbidden mechanism %q in a non-comment line: %s", forbidden, trimmed)
			}
		}
	}
}

func TestWriteHookScript(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "sub", "thlibo-rewrite-cursor.sh")
	if err := WriteHookScript(dest); err != nil {
		t.Fatalf("WriteHookScript: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !reflect.DeepEqual(got, HookScript()) {
		t.Error("written bytes differ from embedded script")
	}
}

// TestMergeHooksJSONFreshFile: the written file matches the Cursor
// schema (top-level version + hooks.preToolUse array of flat
// {matcher, command} objects).
func TestMergeHooksJSONFreshFile(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	hook := filepath.Join(dir, "thlibo-rewrite-cursor.sh")
	if err := MergeHooksJSON(hp, hook); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var root map[string]any
	buf, _ := os.ReadFile(hp)
	if err := json.Unmarshal(buf, &root); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if root["version"] != float64(1) {
		t.Errorf("expected version 1, got %v", root["version"])
	}
	entry := firstPreToolUse(t, root)
	if entry["matcher"] != "Shell" {
		t.Errorf("matcher = %v, want Shell", entry["matcher"])
	}
	if !strings.Contains(entry["command"].(string), "thlibo-rewrite-cursor.sh") {
		t.Errorf("command doesn't point at the hook: %v", entry["command"])
	}
}

// TestMergeHooksJSONPreservesOtherEventsAndKeys: an existing hooks.json
// with other events and a custom version is preserved; we only add our
// preToolUse/Shell entry.
func TestMergeHooksJSONPreservesOtherEventsAndKeys(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	existing := `{
  "version": 1,
  "hooks": {
    "afterFileEdit": [{ "command": "./fmt.sh" }],
    "preToolUse": [{ "matcher": "Read", "command": "./audit.sh" }]
  }
}`
	_ = os.WriteFile(hp, []byte(existing), 0o600)

	hook := filepath.Join(dir, "thlibo-rewrite-cursor.sh")
	if err := MergeHooksJSON(hp, hook); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var root map[string]any
	buf, _ := os.ReadFile(hp)
	_ = json.Unmarshal(buf, &root)

	hooks := root["hooks"].(map[string]any)
	if _, ok := hooks["afterFileEdit"]; !ok {
		t.Error("afterFileEdit event lost")
	}
	pre := hooks["preToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("expected 2 preToolUse entries (the audit one + ours), got %d", len(pre))
	}
	// The unrelated Read audit hook must survive verbatim.
	foundAudit, foundThlibo := false, false
	for _, e := range pre {
		obj := e.(map[string]any)
		cmd, _ := obj["command"].(string)
		if strings.Contains(cmd, "audit.sh") {
			foundAudit = true
		}
		if strings.Contains(cmd, "thlibo-rewrite-cursor.sh") {
			foundThlibo = true
		}
	}
	if !foundAudit {
		t.Error("unrelated Read audit hook was dropped")
	}
	if !foundThlibo {
		t.Error("thlibo hook not added")
	}
}

// TestMergeHooksJSONIdempotent: three merges produce exactly one thlibo
// entry (recognised by the hook-path marker, updated in place).
func TestMergeHooksJSONIdempotent(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	hook := filepath.Join(dir, "thlibo-rewrite-cursor.sh")
	for i := 0; i < 3; i++ {
		if err := MergeHooksJSON(hp, hook); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	buf, _ := os.ReadFile(hp)
	n := strings.Count(string(buf), "thlibo-rewrite-cursor.sh")
	if n != 1 {
		t.Errorf("expected exactly 1 thlibo entry, got %d: %s", n, buf)
	}
}

// TestMergeHooksJSONRejectsInvalidJSON: malformed existing file is not
// clobbered — the merge errors instead of destroying user data.
func TestMergeHooksJSONRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	_ = os.WriteFile(hp, []byte("{ not json"), 0o600)
	if err := MergeHooksJSON(hp, filepath.Join(dir, "thlibo-rewrite-cursor.sh")); err == nil {
		t.Error("expected an error on malformed JSON, got nil")
	}
	// File must be left as-is.
	buf, _ := os.ReadFile(hp)
	if string(buf) != "{ not json" {
		t.Errorf("malformed file was modified: %q", buf)
	}
}

// TestMergeHooksJSONNormalisesBackslashes: a Windows hook path is
// written with forward slashes so bash -c can execute it.
func TestMergeHooksJSONNormalisesBackslashes(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	winPath := `C:\Users\me\.thlibo\hooks\thlibo-rewrite-cursor.sh`
	if err := MergeHooksJSON(hp, winPath); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	buf, _ := os.ReadFile(hp)
	if strings.Contains(string(buf), `\`) {
		t.Errorf("backslashes not normalised: %s", buf)
	}
	if !strings.Contains(string(buf), "C:/Users/me/.thlibo/hooks/thlibo-rewrite-cursor.sh") {
		t.Errorf("forward-slash path missing: %s", buf)
	}
}

// TestMergeHooksJSONPreservesExistingVersion: if the user already set a
// version (e.g. a future 2), we don't override it.
func TestMergeHooksJSONPreservesExistingVersion(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	_ = os.WriteFile(hp, []byte(`{"version": 2, "hooks": {}}`), 0o600)
	if err := MergeHooksJSON(hp, filepath.Join(dir, "thlibo-rewrite-cursor.sh")); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var root map[string]any
	buf, _ := os.ReadFile(hp)
	_ = json.Unmarshal(buf, &root)
	if root["version"] != float64(2) {
		t.Errorf("existing version overwritten: got %v, want 2", root["version"])
	}
}

func firstPreToolUse(t *testing.T, root map[string]any) map[string]any {
	t.Helper()
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatal("no hooks object")
	}
	pre, ok := hooks["preToolUse"].([]any)
	if !ok || len(pre) == 0 {
		t.Fatal("no preToolUse entries")
	}
	return pre[0].(map[string]any)
}
