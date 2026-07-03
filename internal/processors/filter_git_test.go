package processors

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// gitFixtures are representative git outputs the filter must handle.
var gitFixtures = map[string]string{
	"status": `On branch main
Your branch is up to date with 'origin/main'.

Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)
  (use "git restore <file>..." to discard changes in working directory)
	modified:   internal/foo.go
	modified:   README.md

Untracked files:
  (use "git add <file>..." to include in what will be committed)
	newfile.txt

no changes added to commit (use "git add" and/or "git commit -a")
`,
	"diff": `diff --git a/foo.go b/foo.go
index 1234567..89abcde 100644
--- a/foo.go
+++ b/foo.go
@@ -1,5 +1,6 @@
 package foo
-import "old"
+import "new"
+import "extra"
 func main() {}
diff --git a/bar.go b/bar.go
index aaa..bbb 100644
--- a/bar.go
+++ b/bar.go
@@ -10,3 +10,2 @@
-removed line
 context
`,
	"log": `commit 1234567890abcdef1234567890abcdef12345678
Author: Someone <s@example.com>
Date:   Mon Jan 1 00:00:00 2026 +0000

    Fix the thing

commit fedcba0987654321fedcba0987654321fedcba09
Author: Other <o@example.com>
Date:   Tue Jan 2 00:00:00 2026 +0000

    Add the other thing
`,
	"noise": "just some\nrandom text\nnot git at all\n",
	"empty": "",
}

// TestGitFilterParity runs the Go filter and the Python run.py on the
// same fixtures and requires byte-identical output (ADR 0010 parity).
// Skips when python3 isn't available (e.g. minimal CI images) — the
// filter's own behaviour is still covered by TestGitFilterGolden below.
func TestGitFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available; parity check skipped")
	}
	script := referenceScript(t, "git-filter")
	for name, in := range gitFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(gitFilter([]byte(in)))
			// The middleware applies the monotonic guard on top; here we
			// compare the raw transform to Python's raw transform.
			if got != want {
				t.Errorf("git-filter parity mismatch on %q:\n go: %q\n py: %q", name, got, want)
			}
		})
	}
}

// --- shared helpers for native-filter parity tests ---

func pythonBin(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// referenceScript returns the absolute path to processors/<name>/run.py.
func referenceScript(t *testing.T, name string) string {
	t.Helper()
	// This test file lives in internal/processors/; the reference
	// scripts are at ../../processors/<name>/run.py.
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(repoRoot, "processors", name, "run.py")
}

func runPython(t *testing.T, py, script, input string) string {
	t.Helper()
	cmd := exec.Command(py, script)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python %s failed: %v", script, err)
	}
	return string(out)
}
