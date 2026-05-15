package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// WriteHookScript creates the file with stamped contents when absent.
func TestWriteHookScript(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "sub", "thlibo-rewrite.sh")
	result, err := WriteHookScript(dest)
	if err != nil {
		t.Fatalf("WriteHookScript: %v", err)
	}
	if result != WriteResultCreated {
		t.Errorf("first install: result = %v, want Created", result)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read written hook: %v", err)
	}
	// The written file is the stamped version (original + SHA comment),
	// not the raw embedded bytes.
	want := stampedContent(HookScript(), "# thlibo-installed-sha: ")
	if string(got) != string(want) {
		t.Errorf("written contents differ from expected stamped hook")
	}
	// Stamped file must contain the SHA comment.
	if !strings.Contains(string(got), "# thlibo-installed-sha: ") {
		t.Errorf("stamped file missing SHA comment")
	}
}

// WriteHookScript returns Unchanged when called again with no embedded changes.
func TestWriteHookScriptUnchanged(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "thlibo-rewrite.sh")
	if _, err := WriteHookScript(dest); err != nil {
		t.Fatalf("first write: %v", err)
	}
	result, err := WriteHookScript(dest)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if result != WriteResultUnchanged {
		t.Errorf("second install with same embedded: result = %v, want Unchanged", result)
	}
}

// WriteHookScript returns Updated when the file was written by a legacy
// installer that didn't stamp it, and the content exactly matches the
// current embedded bytes (no user edits).
func TestWriteHookScriptLegacyUnstamped(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "thlibo-rewrite.sh")

	// Write the raw embedded bytes with no stamp — mimics what the
	// v0.2.0 installer wrote before stamping was introduced.
	if err := os.WriteFile(dest, HookScript(), 0o700); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}

	result, err := WriteHookScript(dest)
	if err != nil {
		t.Fatalf("WriteHookScript on legacy file: %v", err)
	}
	if result != WriteResultUpdated {
		t.Errorf("legacy unstamped file: result = %v, want Updated (not Conflict)", result)
	}
	// After upgrade the file should have the stamp.
	data, _ := os.ReadFile(dest)
	if !strings.Contains(string(data), "# thlibo-installed-sha: ") {
		t.Errorf("upgraded file missing SHA stamp")
	}
}

// WriteHookScript returns Updated when the embedded content changed but the
// user never edited the file. We simulate an older install by writing a
// file stamped with a different (fake) hash.
func TestWriteHookScriptUpdated(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "thlibo-rewrite.sh")

	// Write a pristine file stamped with a hash that does NOT match
	// the current embedded content — simulates a previous version.
	prefix := "# thlibo-installed-sha: "
	fakeOld := []byte("#!/usr/bin/env bash\necho old\n")
	oldStamped := stampedContent(fakeOld, prefix)
	if err := os.WriteFile(dest, oldStamped, 0o700); err != nil {
		t.Fatalf("seed old file: %v", err)
	}

	result, err := WriteHookScript(dest)
	if err != nil {
		t.Fatalf("WriteHookScript update: %v", err)
	}
	if result != WriteResultUpdated {
		t.Errorf("result = %v, want Updated", result)
	}
}

// WriteHookScript returns Conflict and writes a .new file when the user
// modified the installed hook.
func TestWriteHookScriptConflict(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "thlibo-rewrite.sh")

	// First install.
	if _, err := WriteHookScript(dest); err != nil {
		t.Fatalf("initial install: %v", err)
	}

	// Simulate user edit: append a line. The stored hash is still the
	// original embedded hash, but the on-disk content no longer matches.
	data, _ := os.ReadFile(dest)
	data = append(data, []byte("# user added this line\n")...)
	if err := os.WriteFile(dest, data, 0o700); err != nil {
		t.Fatalf("simulate user edit: %v", err)
	}

	// Now simulate a new embedded version by changing the on-disk stamp
	// to a fake "old" hash so the installer thinks there's a newer version.
	// We achieve this by replacing the SHA comment with a wrong hash.
	modified := strings.ReplaceAll(string(data), hookContentHash(HookScript()), "deadbeef00000000000000000000000000000000000000000000000000000000")
	if err := os.WriteFile(dest, []byte(modified), 0o700); err != nil {
		t.Fatalf("patch stamp: %v", err)
	}

	result, err := WriteHookScript(dest)
	if err != nil {
		t.Fatalf("WriteHookScript conflict: %v", err)
	}
	if result != WriteResultConflict {
		t.Errorf("result = %v, want Conflict", result)
	}
	// .new file must exist with the new content.
	newPath := dest + ".new"
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf(".new file not created: %v", err)
	}
	// Original file must be untouched (still contains user edit).
	after, _ := os.ReadFile(dest)
	if !strings.Contains(string(after), "# user added this line") {
		t.Errorf("user edits clobbered on conflict; file = %q", after)
	}
}

// v0.2 / #16: RemoveHooks drops every thlibo entry (Bash + PS1),
// cleans empty matcher groups, and removes an empty PreToolUse
// array. Unrelated hooks survive untouched.
func TestRemoveHooksDropsThliboAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bashHp := filepath.Join(dir, "thlibo-rewrite.sh")
	ps1Hp := filepath.Join(dir, "thlibo-rewrite.ps1")

	// Seed with both thlibo entries plus an unrelated PreToolUse
	// hook we must not touch.
	root := map[string]any{
		"model": "claude-sonnet-4-6",
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": bashHp},
					},
				},
				map[string]any{
					"matcher": "PowerShell",
					"hooks": []any{
						map[string]any{"type": "command",
							"command": `powershell -NoProfile -ExecutionPolicy Bypass -File "` + ps1Hp + `"`},
					},
				},
				map[string]any{
					"matcher": "Edit|Write",
					"hooks": []any{
						map[string]any{"type": "command", "command": "prettier --write"},
					},
				},
			},
		},
	}
	raw, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(sp, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemoveHooks(sp); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}

	var got map[string]any
	out, _ := os.ReadFile(sp)
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if got["model"] != "claude-sonnet-4-6" {
		t.Errorf("model lost: %v", got["model"])
	}
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("PreToolUse groups = %d after remove, want 1 (the unrelated one): %s", len(pre), out)
	}
	if pre[0].(map[string]any)["matcher"] != "Edit|Write" {
		t.Errorf("unrelated hook damaged: %v", pre[0])
	}
}

// v0.2 / #16: RemoveHooks on a non-existent file is a no-op.
func TestRemoveHooksNoFileIsNoop(t *testing.T) {
	if err := RemoveHooks(filepath.Join(t.TempDir(), "does-not-exist.json")); err != nil {
		t.Fatalf("RemoveHooks should no-op on missing file, got %v", err)
	}
}

// v0.2 / #16: when thlibo is the only PreToolUse entry, the whole
// hooks.PreToolUse key is removed, and an empty hooks object is
// removed too, keeping the settings file tidy.
func TestRemoveHooksTrimsEmptyContainers(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bashHp := filepath.Join(dir, "thlibo-rewrite.sh")

	if err := MergeSettingsFull(sp, bashHp, ""); err != nil {
		t.Fatal(err)
	}
	if err := RemoveHooks(sp); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	out, _ := os.ReadFile(sp)
	_ = json.Unmarshal(out, &got)
	if _, ok := got["hooks"]; ok {
		t.Errorf("hooks container should be removed when empty, got %v", got["hooks"])
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

// v0.2 / #12: WriteHookScriptPS1 writes the stamped PowerShell hook on
// first install and returns WriteResultCreated.
func TestWriteHookScriptPS1(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "thlibo-rewrite.ps1")
	result, err := WriteHookScriptPS1(dest)
	if err != nil {
		t.Fatalf("WriteHookScriptPS1: %v", err)
	}
	if result != WriteResultCreated {
		t.Errorf("first install: result = %v, want Created", result)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := stampedContent(HookScriptPS1(), "# thlibo-installed-sha: ")
	if string(got) != string(want) {
		t.Errorf("written PS1 differs from expected stamped content")
	}
	if !strings.Contains(string(got), "PowerShell") {
		t.Errorf("embedded PS1 missing PowerShell keyword; wrong file?")
	}
}

// WriteHookScriptPS1 is Unchanged on the second install with no embedded changes.
func TestWriteHookScriptPS1Unchanged(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "thlibo-rewrite.ps1")
	if _, err := WriteHookScriptPS1(dest); err != nil {
		t.Fatalf("first write: %v", err)
	}
	result, err := WriteHookScriptPS1(dest)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if result != WriteResultUnchanged {
		t.Errorf("result = %v, want Unchanged", result)
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

// #25: MergeSettingsWithRead adds a Read matcher alongside Bash + PS.
func TestMergeSettingsWithReadRegistersReadMatcher(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bash := filepath.Join(dir, "thlibo-rewrite.sh")
	ps1 := filepath.Join(dir, "thlibo-rewrite.ps1")
	read := filepath.Join(dir, "thlibo-read.sh")
	readPS1 := filepath.Join(dir, "thlibo-read.ps1")

	if err := MergeSettingsWithRead(sp, bash, ps1, read, readPS1); err != nil {
		t.Fatalf("MergeSettingsWithRead: %v", err)
	}
	var got map[string]any
	raw, _ := os.ReadFile(sp)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	matchers := map[string]map[string]any{}
	for _, g := range pre {
		obj := g.(map[string]any)
		matchers[obj["matcher"].(string)] = obj
	}
	if matchers["Read"] == nil {
		t.Fatalf("Read matcher missing; got: %s", raw)
	}
	cmd := matchers["Read"]["hooks"].([]any)[0].(map[string]any)["command"].(string)
	// Platform-dispatched: Windows gets ps1 runner, others get plain script.
	if runtimeIsWindows() {
		if !strings.Contains(cmd, "thlibo-read.ps1") {
			t.Errorf("Windows should pick ps1 Read hook; got %q", cmd)
		}
	} else {
		if !strings.Contains(cmd, "thlibo-read.sh") {
			t.Errorf("non-Windows should pick bash Read hook; got %q", cmd)
		}
	}
}

// v0.4 stage 2: MergeSettingsAll registers Write hooks against both
// "Write" and "Edit" matchers, picking the platform-appropriate
// script the same way Read does.
func TestMergeSettingsAllRegistersWriteAndEditMatchers(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bashWrite := filepath.Join(dir, "thlibo-write.sh")
	ps1Write := filepath.Join(dir, "thlibo-write.ps1")

	if err := MergeSettingsAll(sp, MergeHooks{
		BashWriteHook: bashWrite,
		PS1WriteHook:  ps1Write,
	}); err != nil {
		t.Fatalf("MergeSettingsAll: %v", err)
	}

	var got map[string]any
	raw, _ := os.ReadFile(sp)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	matchers := map[string]map[string]any{}
	for _, g := range pre {
		obj := g.(map[string]any)
		matchers[obj["matcher"].(string)] = obj
	}
	if matchers["Write"] == nil {
		t.Errorf("Write matcher missing; got: %s", raw)
	}
	if matchers["Edit"] == nil {
		t.Errorf("Edit matcher missing; got: %s", raw)
	}
	if matchers["Write"] != nil && matchers["Edit"] != nil {
		// Both matchers should reference the same script — one
		// physical file, two registrations.
		wcmd := matchers["Write"]["hooks"].([]any)[0].(map[string]any)["command"].(string)
		ecmd := matchers["Edit"]["hooks"].([]any)[0].(map[string]any)["command"].(string)
		if wcmd != ecmd {
			t.Errorf("Write and Edit should point at the same script; got %q vs %q", wcmd, ecmd)
		}
	}
}

// v0.4 stage 2: re-running MergeSettingsAll doesn't duplicate Write
// or Edit entries. Same idempotence contract as the Bash matcher.
func TestMergeSettingsAllWriteIdempotent(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bashWrite := filepath.Join(dir, "thlibo-write.sh")
	ps1Write := filepath.Join(dir, "thlibo-write.ps1")

	for i := 0; i < 3; i++ {
		if err := MergeSettingsAll(sp, MergeHooks{
			BashWriteHook: bashWrite,
			PS1WriteHook:  ps1Write,
		}); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}

	raw, _ := os.ReadFile(sp)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)

	writeCount, editCount := 0, 0
	for _, g := range pre {
		obj := g.(map[string]any)
		switch obj["matcher"].(string) {
		case "Write":
			writeCount += len(obj["hooks"].([]any))
		case "Edit":
			editCount += len(obj["hooks"].([]any))
		}
	}
	if writeCount != 1 {
		t.Errorf("Write hooks after 3 merges = %d, want 1", writeCount)
	}
	if editCount != 1 {
		t.Errorf("Edit hooks after 3 merges = %d, want 1", editCount)
	}
}

// v0.4 stage 2: RemoveHooks drops Write+Edit entries the same way
// it drops Bash/PowerShell/Read.
func TestRemoveHooksDropsWriteAndEdit(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")

	// Seed with both Write and Edit matchers pointing at thlibo
	// scripts, plus an unrelated hook that must survive.
	writeScript := filepath.Join(dir, "thlibo-write.sh")
	root := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Write",
					"hooks": []any{
						map[string]any{"type": "command", "command": writeScript},
					},
				},
				map[string]any{
					"matcher": "Edit",
					"hooks": []any{
						map[string]any{"type": "command", "command": writeScript},
						map[string]any{"type": "command", "command": "user-script.sh"},
					},
				},
			},
		},
	}
	raw, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(sp, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveHooks(sp); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}

	out, _ := os.ReadFile(sp)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	hooks := got["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)

	for _, g := range pre {
		obj := g.(map[string]any)
		hooksList, _ := obj["hooks"].([]any)
		for _, h := range hooksList {
			if obj["matcher"].(string) == "Edit" {
				cmd := h.(map[string]any)["command"].(string)
				if strings.Contains(cmd, "thlibo-write") {
					t.Errorf("thlibo Edit entry survived RemoveHooks: %q", cmd)
				}
			}
		}
	}
	// The unrelated user-script.sh on the Edit matcher must
	// survive.
	survived := false
	for _, g := range pre {
		obj := g.(map[string]any)
		if obj["matcher"].(string) == "Edit" {
			for _, h := range obj["hooks"].([]any) {
				if h.(map[string]any)["command"].(string) == "user-script.sh" {
					survived = true
				}
			}
		}
	}
	if !survived {
		t.Errorf("unrelated user-script.sh dropped: %s", out)
	}
}

// #25: RemoveHooks must drop Read-matcher entries as well as Exec ones.
func TestRemoveHooksDropsReadEntries(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")

	// Seed with a Read matcher in both forms + an unrelated hook.
	root := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Read",
					"hooks": []any{
						map[string]any{"type": "command",
							"command": filepath.Join(dir, "thlibo-read.sh")},
					},
				},
				map[string]any{
					"matcher": "Read",
					"hooks": []any{
						map[string]any{"type": "command",
							"command": "powershell -File " + filepath.Join(dir, "thlibo-read.ps1")},
					},
				},
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "some-other-hook"},
					},
				},
			},
		},
	}
	raw, _ := json.MarshalIndent(root, "", "  ")
	_ = os.WriteFile(sp, raw, 0o600)

	if err := RemoveHooks(sp); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(sp)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("want 1 unrelated matcher left, got %d: %s", len(pre), out)
	}
	if pre[0].(map[string]any)["matcher"] != "Bash" {
		t.Errorf("unrelated hook should survive, got %v", pre[0])
	}
}

// #25: InstallCaselogSkill writes SKILL.md at skillsDir/caselog/SKILL.md
// and is idempotent across reinstalls.
func TestInstallCaselogSkill(t *testing.T) {
	dir := t.TempDir()
	result, err := InstallCaselogSkill(dir)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if result != WriteResultCreated {
		t.Errorf("first install = %s, want created", result)
	}
	target := filepath.Join(dir, "caselog", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}

	// Reinstall: unchanged (hash matches).
	result2, err := InstallCaselogSkill(dir)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if result2 != WriteResultUnchanged {
		t.Errorf("second install = %s, want unchanged", result2)
	}

	// User edit = conflict, new version goes to .new, original untouched.
	userEdit := []byte("# user-modified\n\noverride\n")
	if err := os.WriteFile(target, userEdit, 0o600); err != nil {
		t.Fatal(err)
	}
	result3, err := InstallCaselogSkill(dir)
	if err != nil {
		t.Fatalf("third install: %v", err)
	}
	if result3 != WriteResultConflict {
		t.Errorf("third install = %s, want conflict", result3)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(userEdit) {
		t.Errorf("conflict should preserve user edit; got %q", got)
	}
	if _, err := os.Stat(target + ".new"); err != nil {
		t.Errorf("conflict should write .new: %v", err)
	}
}
