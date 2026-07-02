package cursor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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

// runHook executes the embedded hook.sh with the given stdin, using a
// fake `thlibo` on PATH that echoes a fixed rewrite and exits with
// rewriteExit. Returns (stdout, exitCode). Skips on non-bash platforms
// or when bash/jq aren't available, so it exercises the real script
// wherever a POSIX shell exists (CI linux/macOS) without a real thlibo.
func runHook(t *testing.T, stdin, rewriteOut string, rewriteExit int) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook.sh execution test needs a POSIX shell; skipped on windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}

	dir := t.TempDir()
	// Fake `thlibo`: ignores args, prints rewriteOut, exits rewriteExit.
	fake := filepath.Join(dir, "thlibo")
	script := "#!/usr/bin/env bash\nprintf '%s' " + shellQuote(rewriteOut) + "\nexit " + itoa(rewriteExit) + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil { // #nosec G306 -- test shim needs exec bit
		t.Fatal(err)
	}
	hookPath := filepath.Join(dir, "hook.sh")
	if err := WriteHookScript(hookPath); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bash, hookPath)
	cmd.Stdin = strings.NewReader(stdin)
	// Prepend our fake-thlibo dir so the hook finds it first.
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run hook: %v", err)
	}
	return string(out), code
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestHookOutputWrappableShell drives the real hook.sh with a Shell
// preToolUse envelope + a fake thlibo that rewrites the command, and
// asserts the emitted JSON matches Cursor's contract:
// {"permission":"allow","updated_input":{"command":<rewritten>}}.
func TestHookOutputWrappableShell(t *testing.T) {
	stdin := `{"tool_name":"Shell","tool_input":{"command":"git status"},"tool_use_id":"t1"}`
	out, code := runHook(t, stdin, "thlibo exec -- git status", 0)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output not valid JSON: %v (%q)", err, out)
	}
	if resp["permission"] != "allow" {
		t.Errorf("permission = %v, want allow", resp["permission"])
	}
	ui, ok := resp["updated_input"].(map[string]any)
	if !ok {
		t.Fatalf("updated_input missing/not object: %v", resp["updated_input"])
	}
	if ui["command"] != "thlibo exec -- git status" {
		t.Errorf("updated_input.command = %v, want the wrapped command", ui["command"])
	}
	// Must not use unsupported mechanisms.
	if _, bad := resp["decision"]; bad {
		t.Error("response must not carry Codex's decision field")
	}
}

// TestHookOutputPassthrough covers every case that must emit NOTHING and
// exit 0: non-Shell tool, empty command, non-wrappable command (rewrite
// exit 1), and the reserved exit-3 (no ask path on Cursor).
func TestHookOutputPassthrough(t *testing.T) {
	cases := []struct {
		name        string
		stdin       string
		rewriteOut  string
		rewriteExit int
	}{
		{"non-Shell tool", `{"tool_name":"Read","tool_input":{"path":"/x"}}`, "", 0},
		{"empty command", `{"tool_name":"Shell","tool_input":{"command":""}}`, "", 0},
		{"no wrapper (exit 1)", `{"tool_name":"Shell","tool_input":{"command":"echo hi"}}`, "", 1},
		{"reserved ask (exit 3) has no path", `{"tool_name":"Shell","tool_input":{"command":"git status"}}`, "thlibo exec -- git status", 3},
		{"rewrite equals input", `{"tool_name":"Shell","tool_input":{"command":"git status"}}`, "git status", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runHook(t, tc.stdin, tc.rewriteOut, tc.rewriteExit)
			if code != 0 {
				t.Errorf("exit = %d, want 0", code)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("expected no output (passthrough), got %q", out)
			}
		})
	}
}

// TestHookDisabledKillSwitch: THLIBO_DISABLED short-circuits to
// passthrough even for a wrappable command.
func TestHookDisabledKillSwitch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs POSIX shell")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "thlibo")
	_ = os.WriteFile(fake, []byte("#!/usr/bin/env bash\nprintf 'thlibo exec -- git status'\nexit 0\n"), 0o700) // #nosec G306
	hookPath := filepath.Join(dir, "hook.sh")
	if err := WriteHookScript(hookPath); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, hookPath)
	cmd.Stdin = strings.NewReader(`{"tool_name":"Shell","tool_input":{"command":"git status"}}`)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "THLIBO_DISABLED=1")
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("THLIBO_DISABLED should passthrough, got %q", out)
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
