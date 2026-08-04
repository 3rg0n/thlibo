package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	builtins "github.com/3rg0n/thlibo/processors"
)

// Tests for mirror.go — the step that puts the embedded built-in processors
// on disk during `thlibo install`.
//
// Worth testing because two of its properties are silent when broken. If
// the executable bit is wrong, script processors fail at dispatch and the
// middleware fails open, so the user sees uncompressed output with no error
// anywhere. If the re-mirror chmod is dropped, an upgrade leaves stale
// permissions behind and the same thing happens only on upgraded installs,
// which is worse to diagnose than a fresh break.

func TestIsExecutable(t *testing.T) {
	exec := []string{
		"run.py", "run.sh", "filter.exe", "tool.bin", "go.bat", "x.cmd", "hook.ps1",
		"RUN.PY", "Run.Sh", // extension match must be case-insensitive
	}
	for _, name := range exec {
		if !isExecutable(name) {
			t.Errorf("isExecutable(%q) = false, want true", name)
		}
	}
	notExec := []string{
		"processor.yaml", "processor.md", "README", "README.md",
		"notes.txt", "", "py", ".py.bak",
	}
	for _, name := range notExec {
		if isExecutable(name) {
			t.Errorf("isExecutable(%q) = true, want false", name)
		}
	}
}

// TestMirrorFSWritesTreeAndModes uses a synthetic fs.FS rather than the real
// embedded tree, so the assertions are about mirrorFS's behaviour and don't
// change every time a built-in processor is added.
func TestMirrorFSWritesTreeAndModes(t *testing.T) {
	src := fstest.MapFS{
		"compress/processor.md":    {Data: []byte("# prompt")},
		"pdf-to-md/processor.yaml": {Data: []byte("entry: run.py")},
		"pdf-to-md/run.py":         {Data: []byte("print(1)")},
		"pdf-to-md/README.md":      {Data: []byte("docs")},
	}
	dest := t.TempDir()
	if err := mirrorFS(src, ".", dest); err != nil {
		t.Fatalf("mirrorFS: %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join("compress", "processor.md"):    "# prompt",
		filepath.Join("pdf-to-md", "processor.yaml"): "entry: run.py",
		filepath.Join("pdf-to-md", "run.py"):         "print(1)",
	} {
		got, err := os.ReadFile(filepath.Join(dest, path))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}

	if runtime.GOOS == "windows" {
		return // no execute bit to assert
	}
	script, err := os.Stat(filepath.Join(dest, "pdf-to-md", "run.py"))
	if err != nil {
		t.Fatal(err)
	}
	if script.Mode().Perm() != 0o700 {
		t.Errorf("run.py mode = %v, want 0700 — the dispatcher execs this file",
			script.Mode().Perm())
	}
	// A config file getting the execute bit isn't a functional break, but
	// least privilege is the repo's stated posture and 0600 is what the
	// code claims to do.
	cfg, err := os.Stat(filepath.Join(dest, "pdf-to-md", "processor.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode().Perm() != 0o600 {
		t.Errorf("processor.yaml mode = %v, want 0600", cfg.Mode().Perm())
	}
}

// TestMirrorFSIsIdempotentAndRepairsMode covers the reason mirrorFS calls
// Chmod explicitly: os.WriteFile honours its mode argument only when
// creating a file, so re-mirroring over an existing file with the wrong
// permissions would otherwise leave them wrong. That is the upgrade path,
// not a hypothetical.
func TestMirrorFSIsIdempotentAndRepairsMode(t *testing.T) {
	src := fstest.MapFS{"p/run.sh": {Data: []byte("echo hi")}}
	dest := t.TempDir()
	if err := mirrorFS(src, ".", dest); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dest, "p", "run.sh")
	// Change the content first, to prove a re-mirror overwrites rather than
	// skipping an existing file. Before the chmod, because on Windows a
	// 0o400 file carries the read-only attribute and cannot be written even
	// by the test that set it.
	if err := os.WriteFile(target, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Damage the mode only where it means something. mirrorFS never
	// produces a read-only file itself (0600/0700), so this models an
	// externally-modified install rather than a state the mirror creates.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(target, 0o400); err != nil {
			t.Fatal(err)
		}
	}

	if err := mirrorFS(src, ".", dest); err != nil {
		t.Fatalf("second mirrorFS: %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "echo hi" {
		t.Errorf("re-mirror left stale content %q", body)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("re-mirror left mode %v, want 0700 restored", fi.Mode().Perm())
		}
	}
}

// TestMirrorFSLeavesForeignFilesAlone is a documented contract: users drop
// their own processors into the same directory, so the mirror must not
// behave like a sync that prunes what it doesn't recognise.
func TestMirrorFSLeavesForeignFilesAlone(t *testing.T) {
	dest := t.TempDir()
	mine := filepath.Join(dest, "my-processor")
	if err := os.MkdirAll(mine, 0o750); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(mine, "processor.md")
	if err := os.WriteFile(keep, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := mirrorFS(fstest.MapFS{"compress/processor.md": {Data: []byte("x")}}, ".", dest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(keep)
	if err != nil {
		t.Fatalf("user processor was removed by the mirror: %v", err)
	}
	if string(body) != "mine" {
		t.Errorf("user processor was overwritten: %q", body)
	}
}

// TestMirrorFSPropagatesWriteFailure: a mirror into an unwritable location
// must return the error rather than reporting a successful install. The
// installer surfaces this to the user, so swallowing it would produce an
// install that claims success with no processors on disk.
func TestMirrorFSPropagatesWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits don't block writes the same way on Windows")
	}
	dest := t.TempDir()
	if err := os.Chmod(dest, 0o500); err != nil { // r-x: can traverse, can't create
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dest, 0o700) })

	err := mirrorFS(fstest.MapFS{"p/run.sh": {Data: []byte("x")}}, ".", dest)
	if err == nil {
		t.Error("mirrorFS reported success writing into a read-only directory")
	}
}

// TestMirrorBuiltinsMirrorsTheRealTree exercises the exported entry point
// against the actual go:embed FS. Deliberately asserts a property (every
// embedded file lands on disk) rather than a file list, so adding a
// built-in doesn't break the test.
func TestMirrorBuiltinsMirrorsTheRealTree(t *testing.T) {
	dest := t.TempDir()
	if err := MirrorBuiltins(dest); err != nil {
		t.Fatalf("MirrorBuiltins: %v", err)
	}

	var checked int
	err := fs.WalkDir(builtins.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || p == "." {
			return nil
		}
		want, err := fs.ReadFile(builtins.FS, p)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("embedded %s did not reach disk: %v", p, err)
			return nil
		}
		if string(got) != string(want) {
			t.Errorf("embedded %s differs on disk", p)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("walked the embedded FS and found no files; the embed is empty")
	}
	// The tree must contain at least one prompt processor. A silently
	// empty embed would otherwise let every assertion above vacuously pass.
	if _, err := os.Stat(filepath.Join(dest, "compress", "processor.md")); err != nil {
		t.Errorf("compress/processor.md missing from the mirrored tree: %v", err)
	}
}

func TestDefaultProcessorsDirHonoursEnvOverride(t *testing.T) {
	t.Setenv("THLIBO_PROCESSORS_DIR", filepath.Join("custom", "path"))
	if got := DefaultProcessorsDir(); got != filepath.Join("custom", "path") {
		t.Errorf("DefaultProcessorsDir = %q, want the env override", got)
	}
}

func TestDefaultProcessorsDirUsesHome(t *testing.T) {
	t.Setenv("THLIBO_PROCESSORS_DIR", "")
	got := DefaultProcessorsDir()
	if !strings.HasSuffix(got, filepath.Join(".thlibo", "processors")) {
		t.Errorf("DefaultProcessorsDir = %q, want a path ending in .thlibo/processors", got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("DefaultProcessorsDir = %q, want an absolute path", got)
	}
}
