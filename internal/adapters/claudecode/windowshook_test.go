package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forceWindows pins runtimeIsWindows for one test and restores it after,
// so both host behaviours are covered on every CI leg.
func forceWindows(t *testing.T, win bool) {
	t.Helper()
	prev := runtimeIsWindows
	runtimeIsWindows = func() bool { return win }
	t.Cleanup(func() { runtimeIsWindows = prev })
}

// mergeExecHooks runs MergeSettingsAll with both exec scripts and
// returns the settings file's PreToolUse groups keyed by matcher.
func mergeExecHooks(t *testing.T, settingsPath, bash, ps1 string) map[string]string {
	t.Helper()
	if err := MergeSettingsAll(settingsPath, MergeHooks{
		BashExecHook: bash,
		PS1ExecHook:  ps1,
	}); err != nil {
		t.Fatalf("MergeSettingsAll: %v", err)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse settings: %v\n%s", err, raw)
	}
	out := map[string]string{}
	hooks, _ := root["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	for _, g := range pre {
		obj, ok := g.(map[string]any)
		if !ok {
			continue
		}
		matcher, _ := obj["matcher"].(string)
		entries, _ := obj["hooks"].([]any)
		if len(entries) != 1 {
			t.Fatalf("matcher %q has %d entries, want exactly 1:\n%s",
				matcher, len(entries), raw)
		}
		e, _ := entries[0].(map[string]any)
		cmd, _ := e["command"].(string)
		out[matcher] = cmd
	}
	return out
}

// TestWindowsBashMatcherNeverRegistersBareShellScript is the #127
// regression test.
//
// On Windows a `command` of "C:/…/thlibo-rewrite.sh" is not run by bash.
// Claude Code hands it to the shell, which resolves the .sh file
// association — on a Git-for-Windows box that is
// git-bash.exe --no-cd "%L", a GUI terminal launcher. The user saw a
// window titled `/usr/bin/bash --login -i …\thlibo-rewrite.sh` on every
// Bash tool call, and because thlibo fails open the compression never
// ran at all. Where there is no association the hook fails silently
// instead, which is why this went unnoticed.
//
// So: on Windows the Bash matcher's command must be an explicitly
// interpreted .ps1, never a bare script path.
func TestWindowsBashMatcherNeverRegistersBareShellScript(t *testing.T) {
	forceWindows(t, true)
	dir := t.TempDir()
	cmds := mergeExecHooks(t, filepath.Join(dir, "settings.json"),
		filepath.Join(dir, "thlibo-rewrite.sh"),
		filepath.Join(dir, "thlibo-rewrite.ps1"))

	bash, ok := cmds["Bash"]
	if !ok {
		t.Fatalf("Bash matcher missing: %v", cmds)
	}
	if strings.HasSuffix(bash, ".sh") {
		t.Errorf("Bash matcher registered a bare .sh on Windows — the file "+
			"association pops a Git Bash window instead of running the hook: %q", bash)
	}
	if !strings.Contains(bash, "thlibo-rewrite.ps1") {
		t.Errorf("Bash matcher should point at the .ps1 on Windows, got %q", bash)
	}
	if !strings.Contains(bash, "-ExecutionPolicy Bypass") || !strings.Contains(bash, "-File") {
		t.Errorf("Bash matcher command is not an explicit powershell -File invocation: %q", bash)
	}
}

// TestExecMatchersUseOneScriptPerHost: both exec matchers get the same
// script, chosen by host. The Bash and PowerShell tools deliver the
// command in the same field (tool_input.command) and the hook reads only
// that field, so the script's language is independent of the tool's
// shell. Registering the wrong-language script for the host is the bug;
// registering one per host is the fix.
func TestExecMatchersUseOneScriptPerHost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		windows bool
		want    string
		unwant  string
	}{
		{"windows", true, "thlibo-rewrite.ps1", "thlibo-rewrite.sh"},
		{"unix", false, "thlibo-rewrite.sh", "thlibo-rewrite.ps1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forceWindows(t, tc.windows)
			dir := t.TempDir()
			cmds := mergeExecHooks(t, filepath.Join(dir, "settings.json"),
				filepath.Join(dir, "thlibo-rewrite.sh"),
				filepath.Join(dir, "thlibo-rewrite.ps1"))

			for _, matcher := range []string{"Bash", "PowerShell"} {
				cmd, ok := cmds[matcher]
				if !ok {
					t.Fatalf("%s matcher missing: %v", matcher, cmds)
				}
				if !strings.Contains(cmd, tc.want) {
					t.Errorf("%s matcher: want %s, got %q", matcher, tc.want, cmd)
				}
				if strings.Contains(cmd, tc.unwant) {
					t.Errorf("%s matcher: must not reference %s on this host, got %q",
						matcher, tc.unwant, cmd)
				}
			}
		})
	}
}

// TestUnixExecMatchersNeverInvokePowershell: on Unix the PowerShell
// binary is `pwsh`, not `powershell`. Registering
// `powershell -NoProfile -ExecutionPolicy Bypass -File …` there gives a
// command that cannot run, and thlibo fails open — a permanently dead
// hook with no error surfaced. Choosing the .sh per host avoids needing
// a pwsh invocation at all.
func TestUnixExecMatchersNeverInvokePowershell(t *testing.T) {
	forceWindows(t, false)
	dir := t.TempDir()
	cmds := mergeExecHooks(t, filepath.Join(dir, "settings.json"),
		filepath.Join(dir, "thlibo-rewrite.sh"),
		filepath.Join(dir, "thlibo-rewrite.ps1"))

	// Assert on the invocation, not on a substring anywhere in the
	// command: t.TempDir() embeds the test name, so the path itself
	// contains "powershell".
	for matcher, cmd := range cmds {
		if strings.HasPrefix(strings.ToLower(cmd), "powershell") {
			t.Errorf("%s matcher invokes powershell on Unix: %q", matcher, cmd)
		}
		if strings.Contains(cmd, "-ExecutionPolicy") {
			t.Errorf("%s matcher carries a Windows execution-policy flag on Unix: %q", matcher, cmd)
		}
	}
}

// TestMergeReplacesStaleShellEntryOnWindows is the half of #127 that
// decides whether the fix reaches an existing install. The user's
// settings.json already held a bare .sh under the Bash matcher. Marker
// recognition is per hook FILE, so matching only the new .ps1 marker
// would leave the stale .sh entry in place and append the .ps1 beside
// it — both fire, and the Git Bash window keeps appearing. The stale
// entry must be replaced, not joined.
func TestMergeReplacesStaleShellEntryOnWindows(t *testing.T) {
	forceWindows(t, true)
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")

	// Exactly the shape shipped by pre-#127 installs.
	pre := `{
	  "hooks": {
	    "PreToolUse": [
	      {"matcher": "Bash", "hooks": [
	        {"type": "command", "command": "C:/Users/x/.thlibo/hooks/thlibo-rewrite.sh"}]},
	      {"matcher": "*", "hooks": [
	        {"type": "command", "command": "C:/Users/x/.git-ai/bin/git-ai.exe checkpoint claude"}]}
	    ]
	  }
	}`
	if err := os.WriteFile(sp, []byte(pre), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	cmds := mergeExecHooks(t, sp,
		filepath.Join(dir, "thlibo-rewrite.sh"),
		filepath.Join(dir, "thlibo-rewrite.ps1"))

	if strings.Contains(cmds["Bash"], "thlibo-rewrite.sh") {
		t.Errorf("stale .sh entry survived the upgrade: %q", cmds["Bash"])
	}
	if !strings.Contains(cmds["Bash"], "thlibo-rewrite.ps1") {
		t.Errorf("Bash matcher not upgraded to the .ps1: %q", cmds["Bash"])
	}
	// mergeExecHooks already fails if any group holds more than one
	// entry, which is what catches the append-instead-of-replace bug.
	// The unrelated tool's hook must survive.
	if got := cmds["*"]; !strings.Contains(got, "git-ai.exe") {
		t.Errorf("unrelated hook lost: %q", got)
	}
}

// TestMergeIsIdempotentAcrossHostSwitch: a settings file that travels
// between hosts (dotfile repo, WSL vs Windows) must not accumulate one
// entry per host. Each pass leaves exactly one entry per matcher, which
// mergeExecHooks asserts.
func TestMergeIsIdempotentAcrossHostSwitch(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "settings.json")
	bash := filepath.Join(dir, "thlibo-rewrite.sh")
	ps1 := filepath.Join(dir, "thlibo-rewrite.ps1")

	for _, win := range []bool{true, false, true, true, false} {
		forceWindows(t, win)
		cmds := mergeExecHooks(t, sp, bash, ps1)
		want := "thlibo-rewrite.sh"
		if win {
			want = "thlibo-rewrite.ps1"
		}
		if !strings.Contains(cmds["Bash"], want) {
			t.Fatalf("windows=%v: Bash matcher = %q, want %s", win, cmds["Bash"], want)
		}
	}
}
