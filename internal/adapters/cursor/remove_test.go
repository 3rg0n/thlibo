package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hooksJSONWithForeignEntry is the shape a real ~/.cursor/hooks.json holds
// after thlibo installs beside another tool: thlibo's two preToolUse
// entries, one foreign entry, and an unrelated event.
const hooksJSONWithForeignEntry = `{
  "version": 1,
  "hooks": {
    "preToolUse": [
      { "matcher": "Shell", "command": "/h/.taco/hook.sh" },
      { "matcher": "Shell", "command": "/h/.thlibo/hooks/thlibo-rewrite-cursor.sh" },
      { "matcher": "Read", "command": "/h/.thlibo/hooks/thlibo-read-cursor.sh" }
    ],
    "afterShellExecution": [
      { "command": "/h/.taco/after.sh" }
    ]
  }
}`

func writeHooks(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRemoveHooks_StripsEntriesKeepsForeign is the #137 case for Cursor:
// both thlibo entries go, every other tool's entry and event stays, and
// the file stays valid JSON with its required version key.
func TestRemoveHooks_StripsEntriesKeepsForeign(t *testing.T) {
	p := writeHooks(t, hooksJSONWithForeignEntry)
	if err := RemoveHooks(p, ""); err != nil {
		t.Fatal(err)
	}
	buf, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if containsThliboMarker(string(buf)) {
		t.Errorf("hooks.json still names a thlibo hook:\n%s", buf)
	}

	var root map[string]any
	if err := json.Unmarshal(buf, &root); err != nil {
		t.Fatalf("hooks.json is no longer valid JSON: %v\n%s", err, buf)
	}
	if root["version"] == nil {
		t.Error("the top-level version key was dropped; Cursor requires it")
	}
	hooks, _ := root["hooks"].(map[string]any)
	pre, _ := hooks["preToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("preToolUse has %d entries, want 1 (the foreign one):\n%s", len(pre), buf)
	}
	if cmd, _ := pre[0].(map[string]any)["command"].(string); cmd != "/h/.taco/hook.sh" {
		t.Errorf("surviving preToolUse entry = %q, want the foreign hook", cmd)
	}
	if _, ok := hooks["afterShellExecution"]; !ok {
		t.Errorf("an unrelated event was dropped:\n%s", buf)
	}
}

// TestRemoveHooks_DeletesScripts: the registration and the script go
// together. Leaving the script is litter; leaving the registration is
// #137, where Cursor keeps running a hook uninstall said it removed.
func TestRemoveHooks_DeletesScripts(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o750); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(hookDir, shellHookMarker)
	read := filepath.Join(hookDir, readHookMarker)
	keep := filepath.Join(hookDir, "thlibo-rewrite-codex.sh") // another adapter's
	for _, p := range []string{shell, read, keep} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(p, []byte(hooksJSONWithForeignEntry), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemoveHooks(p, hookDir); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{shell, read} {
		if _, err := os.Stat(s); !os.IsNotExist(err) {
			t.Errorf("%s survived; stat err = %v", filepath.Base(s), err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("RemoveHooks deleted another adapter's script: %v", err)
	}
}

// TestRemoveHooks_WindowsBashWrappedCommand: on Windows the command is
// `"<bash>" "<hook>"`, so the marker is mid-string. Matching the whole
// value would leave the entry registered on exactly the host that needs
// the wrapper.
func TestRemoveHooks_WindowsBashWrappedCommand(t *testing.T) {
	p := writeHooks(t, `{"version":1,"hooks":{"preToolUse":[
	  {"matcher":"Shell","command":"\"C:/Program Files/Git/bin/bash.exe\" \"C:/Users/u/.thlibo/hooks/thlibo-rewrite-cursor.sh\""}]}}`)
	if err := RemoveHooks(p, ""); err != nil {
		t.Fatal(err)
	}
	buf, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if containsThliboMarker(string(buf)) {
		t.Errorf("bash-wrapped thlibo entry survived:\n%s", buf)
	}
	if strings.Contains(string(buf), "preToolUse") {
		t.Errorf("the emptied preToolUse array should be dropped:\n%s", buf)
	}
}

// TestRemoveHooks_BackslashCommand: a command stored with Windows
// separators must still match. normalisePath is why containsThliboMarker
// sees it.
func TestRemoveHooks_BackslashCommand(t *testing.T) {
	p := writeHooks(t, `{"version":1,"hooks":{"preToolUse":[
	  {"matcher":"Read","command":"C:\\Users\\u\\.thlibo\\hooks\\thlibo-read-cursor.sh"}]}}`)
	if err := RemoveHooks(p, ""); err != nil {
		t.Fatal(err)
	}
	buf, _ := os.ReadFile(p)
	if containsThliboMarker(string(buf)) {
		t.Errorf("backslash-path entry survived:\n%s", buf)
	}
}

// TestRemoveHooks_NoThliboEntry: a file holding only another tool's hooks
// comes back byte-for-byte. We never reformat a file we don't own.
func TestRemoveHooks_NoThliboEntry(t *testing.T) {
	const foreign = `{"version":1,"hooks":{"preToolUse":[{"matcher":"Shell","command":"/h/.taco/hook.sh"}]}}`
	p := writeHooks(t, foreign)
	if err := RemoveHooks(p, ""); err != nil {
		t.Fatal(err)
	}
	buf, _ := os.ReadFile(p)
	if string(buf) != foreign {
		t.Errorf("a foreign-only hooks.json was rewritten:\n got: %s\nwant: %s", buf, foreign)
	}
}

// TestRemoveHooks_Malformed: refuse to touch a file we can't parse, and
// don't report an error for it either — install refuses the same file, so
// the user's editor is where it gets fixed. Losing their hook config to a
// stray comma on the way out is the worse outcome.
func TestRemoveHooks_Malformed(t *testing.T) {
	const broken = `{"version":1,"hooks":{"preToolUse":[{"command":"/h/.thlibo/hooks/thlibo-rewrite-cursor.sh"},]}}`
	p := writeHooks(t, broken)
	if err := RemoveHooks(p, ""); err != nil {
		t.Fatalf("malformed hooks.json = %v, want nil", err)
	}
	buf, _ := os.ReadFile(p)
	if string(buf) != broken {
		t.Errorf("malformed hooks.json was rewritten:\n%s", buf)
	}
}

// TestRemoveHooks_Missing: absent is the desired end state.
func TestRemoveHooks_Missing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hooks.json")
	if err := RemoveHooks(p, ""); err != nil {
		t.Fatalf("absent hooks.json = %v, want nil", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("removal created %s", p)
	}
}

// TestMergeThenRemoveLeavesNoThliboRefs pairs the two halves: whatever
// MergeHooksJSON writes, RemoveHooks must be able to find. A change to the
// entry shape that skipped the remover would reintroduce #137.
func TestMergeThenRemoveLeavesNoThliboRefs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hooks.json")
	hookDir := filepath.Join(dir, "hooks")
	if err := MergeHooksJSON(p,
		filepath.Join(hookDir, shellHookMarker),
		filepath.Join(hookDir, readHookMarker)); err != nil {
		t.Fatal(err)
	}
	buf, _ := os.ReadFile(p)
	if !containsThliboMarker(string(buf)) {
		t.Fatalf("merge installed nothing:\n%s", buf)
	}
	if err := RemoveHooks(p, hookDir); err != nil {
		t.Fatal(err)
	}
	buf, _ = os.ReadFile(p)
	if containsThliboMarker(string(buf)) {
		t.Errorf("remove missed what merge wrote:\n%s", buf)
	}
}
