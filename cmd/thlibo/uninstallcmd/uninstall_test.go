package uninstallcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/3rg0n/thlibo/internal/adapters/codex"
	"github.com/3rg0n/thlibo/internal/adapters/cursor"
)

// setHome points os.UserHomeDir() at dir for the duration of the test.
// On Windows Go reads USERPROFILE; elsewhere HOME. Set both so the test
// is cross-platform.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
}

// TestUninstallCopilotFlagAccepted: `uninstall --copilot` must not be
// rejected as an unknown flag (#75). Before the fix it returned
// ExitUsage ("flag provided but not defined: -copilot").
func TestUninstallCopilotFlagAccepted(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	hookDir := filepath.Join(home, ".thlibo", "hooks")
	settings := filepath.Join(home, ".claude", "settings.json")
	code := Run([]string{
		"--copilot", "--dry-run",
		"--skip-autostart",
		"--hook-dir", hookDir,
		"--settings", settings,
	})
	if code != ExitOK {
		t.Fatalf("uninstall --copilot --dry-run exit = %d, want %d (flag must be accepted)", code, ExitOK)
	}
}

// TestUninstallRemovesCopilotHookFile: a real `uninstall` deletes
// ~/.copilot/hooks/thlibo.json and leaves a co-resident other-tool file
// (git-ai.json) byte-identical.
func TestUninstallRemovesCopilotHookFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	copilotHooks := filepath.Join(home, ".copilot", "hooks")
	if err := os.MkdirAll(copilotHooks, 0o750); err != nil {
		t.Fatal(err)
	}
	thliboJSON := filepath.Join(copilotHooks, "thlibo.json")
	gitAI := filepath.Join(copilotHooks, "git-ai.json")
	gitAIContent := []byte(`{"hooks":{"PreToolUse":[{"type":"command","command":"git-ai"}]}}`)
	if err := os.WriteFile(thliboJSON, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gitAI, gitAIContent, 0o600); err != nil {
		t.Fatal(err)
	}

	hookDir := filepath.Join(home, ".thlibo", "hooks")
	settings := filepath.Join(home, ".claude", "settings.json")
	code := Run([]string{
		"--copilot",
		"--skip-autostart",
		"--hook-dir", hookDir,
		"--settings", settings,
	})
	if code != ExitOK {
		t.Fatalf("uninstall exit = %d, want %d", code, ExitOK)
	}

	if _, err := os.Stat(thliboJSON); !os.IsNotExist(err) {
		t.Errorf("thlibo.json should be removed; stat err = %v", err)
	}
	got, err := os.ReadFile(gitAI)
	if err != nil {
		t.Fatalf("co-resident git-ai.json was removed: %v", err)
	}
	if string(got) != string(gitAIContent) {
		t.Errorf("git-ai.json was modified:\n got: %s\nwant: %s", got, gitAIContent)
	}
}

// TestUninstallDryRunMentionsCopilot: the dry-run plan must name the
// Copilot hook file so a user can see it'll be removed (#75).
func TestUninstallDryRunMentionsCopilot(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	code := Run([]string{
		"--dry-run", "--skip-autostart",
		"--hook-dir", filepath.Join(home, ".thlibo", "hooks"),
		"--settings", filepath.Join(home, ".claude", "settings.json"),
	})
	_ = w.Close()
	os.Stdout = oldStdout

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	plan := string(buf[:n])

	if code != ExitOK {
		t.Fatalf("dry-run exit = %d, want %d", code, ExitOK)
	}
	if !contains(plan, "thlibo.json") || !contains(plan, "Copilot") {
		t.Errorf("dry-run plan must mention the Copilot hook file, got:\n%s", plan)
	}
}

// TestUninstallCodexCursorFlagsAccepted: `uninstall --codex` / `--cursor`
// and the three path overrides must parse. Before #137 they didn't exist,
// so the obvious inverse of the documented install command died at flag
// parsing with exit 2 and removed nothing.
func TestUninstallCodexCursorFlagsAccepted(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	code := Run([]string{
		"--codex", "--cursor", "--copilot", "--dry-run", "--skip-autostart",
		"--hook-dir", filepath.Join(home, ".thlibo", "hooks"),
		"--settings", filepath.Join(home, ".claude", "settings.json"),
		"--codex-hooks", filepath.Join(home, "alt", "config.toml"),
		"--cursor-hooks", filepath.Join(home, "alt", "hooks.json"),
		"--copilot-hooks", filepath.Join(home, "alt", "thlibo.json"),
	})
	if code != ExitOK {
		t.Fatalf("uninstall with the Codex/Cursor flags exit = %d, want %d", code, ExitOK)
	}
}

// TestUninstallRemovesCodexAndCursorHooks is #137 end to end.
//
// The configs are written by the same adapter functions install calls, so
// the test can't drift from the real installed shape. After uninstall,
// neither config may name a thlibo script and neither script may remain —
// otherwise the client keeps firing the hook, which keeps working, because
// the scripts resolve `thlibo` from PATH and uninstall doesn't remove the
// binary. Each config also holds another tool's hook, which must survive.
func TestUninstallRemovesCodexAndCursorHooks(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	hookDir := filepath.Join(home, ".thlibo", "hooks")
	if err := os.MkdirAll(hookDir, 0o750); err != nil {
		t.Fatal(err)
	}
	codexConfig := filepath.Join(home, ".codex", "config.toml")
	cursorHooks := filepath.Join(home, ".cursor", "hooks.json")

	// Another tool's inline Codex hook, in place before thlibo arrives.
	if err := os.MkdirAll(filepath.Dir(codexConfig), 0o750); err != nil {
		t.Fatal(err)
	}
	const foreignCodex = "[[hooks.PostToolUse]]\nmatcher = \"^Bash$\"\n\n[[hooks.PostToolUse.hooks]]\ntype = \"command\"\ncommand = '/h/git-ai-hook'\n"
	if err := os.WriteFile(codexConfig, []byte(foreignCodex), 0o600); err != nil {
		t.Fatal(err)
	}

	codexHook := filepath.Join(hookDir, codex.HookFileName())
	if err := codex.WriteHookScript(codexHook); err != nil {
		t.Fatal(err)
	}
	if err := codex.MergeConfigTOMLHook(codexConfig, codexHook); err != nil {
		t.Fatal(err)
	}
	if err := codex.EnableHooksFeatureFlag(codexConfig); err != nil {
		t.Fatal(err)
	}

	cursorShell := filepath.Join(hookDir, "thlibo-rewrite-cursor.sh")
	cursorRead := filepath.Join(hookDir, "thlibo-read-cursor.sh")
	if err := cursor.WriteHookScript(cursorShell); err != nil {
		t.Fatal(err)
	}
	if err := cursor.WriteReadHookScript(cursorRead); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cursorHooks), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorHooks, []byte(`{"version":1,"hooks":{"preToolUse":[{"matcher":"Shell","command":"/h/.taco/hook.sh"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cursor.MergeHooksJSON(cursorHooks, cursorShell, cursorRead); err != nil {
		t.Fatal(err)
	}

	code := Run([]string{
		"--skip-autostart",
		"--hook-dir", hookDir,
		"--settings", filepath.Join(home, ".claude", "settings.json"),
	})
	if code != ExitOK {
		t.Fatalf("uninstall exit = %d, want %d", code, ExitOK)
	}

	for _, f := range []struct{ label, path string }{
		{"Codex config.toml", codexConfig},
		{"Cursor hooks.json", cursorHooks},
	} {
		buf, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("%s: %v", f.label, err)
		}
		got := string(buf)
		for _, marker := range []string{"thlibo-rewrite-codex", "thlibo-rewrite-cursor", "thlibo-read-cursor"} {
			if contains(got, marker) {
				t.Errorf("%s still registers %s after uninstall:\n%s", f.label, marker, got)
			}
		}
	}
	for _, p := range []string{codexHook, cursorShell, cursorRead} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall; stat err = %v", filepath.Base(p), err)
		}
	}

	cfg, err := os.ReadFile(codexConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(cfg), "command = '/h/git-ai-hook'") {
		t.Errorf("uninstall dropped another tool's Codex hook:\n%s", cfg)
	}
	// The feature flag stays: without it Codex ignores every hook in the
	// layer, including the one above.
	if !contains(string(cfg), "hooks = true") {
		t.Errorf("uninstall cleared [features] hooks = true, disabling other tools' hooks:\n%s", cfg)
	}
	ch, err := os.ReadFile(cursorHooks)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(ch), "/h/.taco/hook.sh") {
		t.Errorf("uninstall dropped another tool's Cursor hook:\n%s", ch)
	}
}

// TestUninstallDryRunMentionsCodexAndCursor: the plan must name both
// configs, so a user can see what uninstall will touch before it does.
func TestUninstallDryRunMentionsCodexAndCursor(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	code := Run([]string{
		"--dry-run", "--skip-autostart",
		"--hook-dir", filepath.Join(home, ".thlibo", "hooks"),
		"--settings", filepath.Join(home, ".claude", "settings.json"),
	})
	_ = w.Close()
	os.Stdout = oldStdout

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	plan := string(buf[:n])

	if code != ExitOK {
		t.Fatalf("dry-run exit = %d, want %d", code, ExitOK)
	}
	for _, want := range []string{"Codex", "config.toml", "Cursor", "hooks.json"} {
		if !contains(plan, want) {
			t.Errorf("dry-run plan must mention %q, got:\n%s", want, plan)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
