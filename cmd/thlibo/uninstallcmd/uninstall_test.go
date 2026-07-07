package uninstallcmd

import (
	"os"
	"path/filepath"
	"testing"
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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
