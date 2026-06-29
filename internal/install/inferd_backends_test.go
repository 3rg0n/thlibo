package install

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsPrereleaseTag locks the predicate that keeps thlibo from
// installing an RC inferd: inferd ships -rc tags without GitHub's
// prerelease flag, so tag-suffix detection is the real gate (#47
// follow-up).
func TestIsPrereleaseTag(t *testing.T) {
	cases := map[string]bool{
		"v0.5.0":        false,
		"v0.5.1":        false,
		"v1.0.0":        false,
		"0.5.0":         false, // no leading v
		"v0.5.1-rc.1":   true,
		"v0.5.0-rc.1":   true,
		"v1.0.0-beta.2": true,
		"v0.4.0-alpha":  true,
	}
	for tag, want := range cases {
		if got := isPrereleaseTag(tag); got != want {
			t.Errorf("isPrereleaseTag(%q) = %v, want %v", tag, got, want)
		}
	}
}

// TestCopyBackends verifies the ggml backend libs are copied as
// siblings of the daemon binary — the step whose absence aborted the
// macOS launchagent script (#47).
func TestCopyBackends(t *testing.T) {
	extract := t.TempDir()
	dst := t.TempDir()

	// Simulate the tarball's backends/ subdir with a couple of libs.
	backends := filepath.Join(extract, "backends")
	if err := os.MkdirAll(backends, 0o755); err != nil {
		t.Fatal(err)
	}
	libs := []string{"libllama.dylib", "libggml-base.dylib", "libggml-metal.so"}
	for _, l := range libs {
		if err := os.WriteFile(filepath.Join(backends, l), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A nested dir should be skipped (we only copy files).
	if err := os.MkdirAll(filepath.Join(backends, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyBackends(extract, dst); err != nil {
		t.Fatalf("copyBackends: %v", err)
	}
	for _, l := range libs {
		p := filepath.Join(dst, l)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s copied next to binary, missing: %v", l, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "nested")); !os.IsNotExist(err) {
		t.Errorf("nested dir should not have been copied")
	}
}

// TestCopyBackendsNoSubdir is a no-op (and no error) when the tarball
// has no backends/ dir — older layouts that already ship libs beside
// the binary.
func TestCopyBackendsNoSubdir(t *testing.T) {
	extract := t.TempDir() // no backends/ inside
	dst := t.TempDir()
	if err := copyBackends(extract, dst); err != nil {
		t.Errorf("copyBackends with no backends/ should be a no-op, got: %v", err)
	}
}
