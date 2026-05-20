package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withFakeHome redirects $HOME (or $USERPROFILE on Windows) to a
// temp dir so MigrateFromV05 sees a controlled layout. Restores
// the original on cleanup. Also overrides APPDATA / LOCALAPPDATA /
// XDG_DATA_HOME so the migration probes only the fake tree, not
// the real user's autostart folders or shared model store.
func withFakeHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", d)
		t.Setenv("LOCALAPPDATA", filepath.Join(d, "AppData", "Local"))
		t.Setenv("APPDATA", filepath.Join(d, "AppData", "Roaming"))
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(d, ".local", "share"))
	return d
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestMigrateNoOpOnFreshInstall(t *testing.T) {
	withFakeHome(t)
	r, err := MigrateFromV05()
	if err != nil {
		t.Fatalf("MigrateFromV05 on empty home: %v", err)
	}
	if r.HasWork() {
		t.Errorf("expected no work on fresh install, got %+v", r)
	}
}

func TestMigrateMovesModelToSharedStore(t *testing.T) {
	home := withFakeHome(t)
	oldGGUF := filepath.Join(home, ".thlibo", "models", "gemma-4-e4b-ud-q4-k-xl.gguf")
	writeFile(t, oldGGUF, []byte("fake gguf bytes for test")) // 24 B placeholder

	r, err := MigrateFromV05()
	if err != nil {
		t.Fatalf("MigrateFromV05: %v", err)
	}
	if r.ModelMovedFrom != oldGGUF {
		t.Errorf("ModelMovedFrom = %q; want %q", r.ModelMovedFrom, oldGGUF)
	}
	expected := filepath.Join(SharedModelsDir(), "gemma-4-e4b-ud-q4-k-xl.gguf")
	if r.ModelMovedTo != expected {
		t.Errorf("ModelMovedTo = %q; want %q", r.ModelMovedTo, expected)
	}
	if _, err := os.Stat(oldGGUF); err == nil {
		t.Errorf("old GGUF still exists at %s", oldGGUF)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("new GGUF missing at %s: %v", expected, err)
	}
	if !r.RemovedModelsDir {
		t.Errorf("expected RemovedModelsDir=true after empty-after-move")
	}
}

func TestMigrateIdempotentOnSecondRun(t *testing.T) {
	home := withFakeHome(t)
	oldGGUF := filepath.Join(home, ".thlibo", "models", "gemma-4-e4b-ud-q4-k-xl.gguf")
	writeFile(t, oldGGUF, []byte("fake gguf bytes for test"))

	if _, err := MigrateFromV05(); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	// Second run: nothing left to do.
	r, err := MigrateFromV05()
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if r.HasWork() {
		t.Errorf("second run should be no-op, got %+v", r)
	}
}

func TestMigrateRemovesDaemonBinary(t *testing.T) {
	home := withFakeHome(t)
	bin := filepath.Join(home, ".local", "bin", thlibodBinName())
	writeFile(t, bin, []byte("\x7fELF placeholder"))

	r, err := MigrateFromV05()
	if err != nil {
		t.Fatalf("MigrateFromV05: %v", err)
	}
	if !r.RemovedDaemonBin {
		t.Errorf("expected RemovedDaemonBin=true")
	}
	if _, err := os.Stat(bin); err == nil {
		t.Errorf("thlibod binary still exists at %s", bin)
	}
}

func TestMigrateRemovesEngineBinary(t *testing.T) {
	home := withFakeHome(t)
	exe := "thlibo-engine"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	engine := filepath.Join(home, ".local", "bin", exe)
	writeFile(t, engine, []byte("APE polyglot placeholder"))

	r, err := MigrateFromV05()
	if err != nil {
		t.Fatalf("MigrateFromV05: %v", err)
	}
	if !r.RemovedEngineBin {
		t.Errorf("expected RemovedEngineBin=true")
	}
	if _, err := os.Stat(engine); err == nil {
		t.Errorf("thlibo-engine still exists at %s", engine)
	}
}

func TestMigrateRemovesDaemonLogs(t *testing.T) {
	home := withFakeHome(t)
	log := filepath.Join(home, ".thlibo", "logs", "thlibod.ndjson")
	writeFile(t, log, []byte(`{"event":"start"}`+"\n"))

	r, err := MigrateFromV05()
	if err != nil {
		t.Fatalf("MigrateFromV05: %v", err)
	}
	if !r.RemovedLogsDir {
		t.Errorf("expected RemovedLogsDir=true")
	}
	if _, err := os.Stat(filepath.Dir(log)); err == nil {
		t.Errorf("logs dir still exists at %s", filepath.Dir(log))
	}
}

func TestMigratePreservesProcessorsAndConfig(t *testing.T) {
	home := withFakeHome(t)
	// Drop fixtures the migration MUST NOT touch.
	keepers := []string{
		filepath.Join(home, ".thlibo", "processors", "git-filter", "run.py"),
		filepath.Join(home, ".thlibo", "hooks", "thlibo-rewrite.sh"),
		filepath.Join(home, ".thlibo", "config.yaml"),
		filepath.Join(home, ".thlibo", "state", "update-check.json"),
	}
	for _, p := range keepers {
		writeFile(t, p, []byte("preserved"))
	}
	// Trigger work too, so the migration actually runs.
	writeFile(t, filepath.Join(home, ".thlibo", "models", "x.gguf"), []byte("model"))

	if _, err := MigrateFromV05(); err != nil {
		t.Fatalf("MigrateFromV05: %v", err)
	}
	for _, p := range keepers {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s should be preserved but is missing: %v", p, err)
		}
	}
}

func TestSharedModelsDirRespectsXDG(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_DATA_HOME is Linux-only")
	}
	d := t.TempDir()
	t.Setenv("XDG_DATA_HOME", d)
	got := SharedModelsDir()
	want := filepath.Join(d, "models")
	if got != want {
		t.Errorf("SharedModelsDir = %q; want %q", got, want)
	}
}
