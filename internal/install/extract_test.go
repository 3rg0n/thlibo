package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tests for the archive-extraction path: stripLeadingComponent, safeJoin,
// extractTarGz and extractZip.
//
// Why this path and not something easier to reach: these four functions are
// the only place in thlibo that writes attacker-influenced *filenames* to
// disk. `thlibo install` downloads an inferd release archive over the
// network and unpacks it into the user's home directory, so a member named
// `../../../.ssh/authorized_keys` is a file-overwrite primitive if safeJoin
// is wrong. Cosign verification and the SHA-256 check are the first line of
// defence, but tryCosignVerify is best-effort (it degrades to a warning when
// cosign isn't installed), so the traversal guard is load-bearing on its
// own and was at 0% coverage.
//
// These construct archives in-process rather than committing fixture files:
// a malicious tarball in the repo would get flagged by scanners and is
// harder to read than the code that builds it.

// tarGz writes a .tar.gz containing the given entries and returns its path.
// Names are used verbatim so a test can supply a traversal payload.
func tarGz(t *testing.T, entries []tarEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := byte(tar.TypeReg)
		if e.dir {
			typ = tar.TypeDir
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Typeflag: typ,
			Mode:     0o644,
			Size:     int64(len(e.body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type tarEntry struct {
	name string
	body string
	dir  bool
}

// zipOf writes a .zip containing the given entries and returns its path.
func zipOf(t *testing.T, entries []tarEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		name := e.name
		if e.dir && !strings.HasSuffix(name, "/") {
			name += "/" // zip signals a directory with a trailing slash
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStripLeadingComponent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// The shape every real inferd asset has: one version-stamped
		// wrapper directory that must not appear in the install dir.
		{"wrapper dir stripped", "inferd-v0.6.0-linux-amd64/inferd", "inferd"},
		{"nested path keeps depth", "inferd-v0.6.0/backends/libggml.so", filepath.Join("backends", "libggml.so")},
		{"leading ./ ignored", "./inferd-v0.6.0/inferd", "inferd"},
		// A bare filename has no wrapper to strip. Returning "" (skip)
		// rather than the name itself is deliberate: a real asset always
		// has the wrapper, so a top-level file is not something to unpack.
		{"bare filename skipped", "inferd", ""},
		{"empty skipped", "", ""},
		{"wrapper with nothing after it skipped", "inferd-v0.6.0/", ""},
		{"only a slash skipped", "/", ""},
		// Traversal survives stripping — by design. stripLeadingComponent
		// removes one component and nothing more; safeJoin is what refuses
		// the result. Asserting it here documents that this function is NOT
		// the security boundary, so nobody later "fixes" the boundary by
		// editing this one.
		{"traversal not the concern of this function", "wrapper/../../etc/passwd",
			filepath.Join("..", "..", "etc", "passwd")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripLeadingComponent(tc.in); got != tc.want {
				t.Errorf("stripLeadingComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSafeJoinAcceptsPathsInsideBase(t *testing.T) {
	base := t.TempDir()
	for _, rel := range []string{
		"inferd",
		filepath.Join("backends", "libggml.so"),
		filepath.Join("a", "b", "c", "d.txt"),
		// Traversal that stays within base after cleaning is fine: the
		// guard is about the resolved destination, not the spelling.
		filepath.Join("a", "..", "b"),
	} {
		got, err := safeJoin(base, rel)
		if err != nil {
			t.Errorf("safeJoin(base, %q) errored: %v", rel, err)
			continue
		}
		if !strings.HasPrefix(got, base) {
			t.Errorf("safeJoin(base, %q) = %q, outside base %q", rel, got, base)
		}
	}
}

func TestSafeJoinRejectsEscapes(t *testing.T) {
	base := t.TempDir()
	escapes := []string{
		filepath.Join("..", "escaped.txt"),
		filepath.Join("..", "..", "..", "etc", "passwd"),
		filepath.Join("a", "..", "..", "escaped.txt"),
	}
	for _, rel := range escapes {
		got, err := safeJoin(base, rel)
		if err == nil {
			t.Errorf("safeJoin(base, %q) = %q with no error; traversal accepted", rel, got)
			continue
		}
		if !strings.Contains(err.Error(), "escapes extract dir") {
			t.Errorf("safeJoin(base, %q) error = %v, want an escape complaint", rel, err)
		}
	}
}

// TestSafeJoinRejectsSiblingPrefix covers the off-by-one that a naive
// strings.HasPrefix(abs, baseAbs) check would let through: a *sibling*
// directory whose name starts with the base's name (base "…/x", target
// "…/x-evil") shares the string prefix without being inside it. safeJoin
// compares with separators appended, which is what makes this fail
// correctly — worth a test because the bug is invisible on inspection.
func TestSafeJoinRejectsSiblingPrefix(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "extract")
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := safeJoin(base, filepath.Join("..", "extract-evil", "f.txt")); err == nil {
		t.Error("safeJoin accepted a sibling dir sharing the base's string prefix")
	}
}

// TestSafeJoinAcceptsBaseItself: an entry resolving to base exactly (".")
// is not an escape. Called out because the prefix comparison needs the
// explicit abs == baseAbs arm to get this right.
func TestSafeJoinAcceptsBaseItself(t *testing.T) {
	base := t.TempDir()
	if _, err := safeJoin(base, "."); err != nil {
		t.Errorf("safeJoin(base, \".\") errored: %v", err)
	}
}

func TestExtractTarGzWritesFilesAndStripsWrapper(t *testing.T) {
	src := tarGz(t, []tarEntry{
		{name: "inferd-v0.6.0-linux-amd64/", dir: true},
		{name: "inferd-v0.6.0-linux-amd64/inferd", body: "#!/bin/sh\n"},
		{name: "inferd-v0.6.0-linux-amd64/backends/", dir: true},
		{name: "inferd-v0.6.0-linux-amd64/backends/libggml.so", body: "ELF"},
	})
	dst := t.TempDir()
	if err := extractTarGz(src, dst); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dst, "inferd"))
	if err != nil {
		t.Fatalf("wrapper directory was not stripped: %v", err)
	}
	if string(body) != "#!/bin/sh\n" {
		t.Errorf("inferd body = %q", body)
	}
	if body, err := os.ReadFile(filepath.Join(dst, "backends", "libggml.so")); err != nil {
		t.Errorf("nested member missing: %v", err)
	} else if string(body) != "ELF" {
		t.Errorf("libggml.so body = %q", body)
	}
	// The wrapper name must not survive anywhere: a nested copy would mean
	// `thlibo install` puts the binary one directory deeper than the
	// installer expects.
	if _, err := os.Stat(filepath.Join(dst, "inferd-v0.6.0-linux-amd64")); err == nil {
		t.Error("wrapper directory was recreated inside the extract dir")
	}
}

// TestExtractTarGzRefusesTraversal is the one that matters: a member named
// to escape the extract dir must fail the extraction, and must not have
// written the file.
func TestExtractTarGzRefusesTraversal(t *testing.T) {
	src := tarGz(t, []tarEntry{
		{name: "wrapper/../../pwned.txt", body: "owned"},
	})
	parent := t.TempDir()
	dst := filepath.Join(parent, "extract")
	if err := os.MkdirAll(dst, 0o750); err != nil {
		t.Fatal(err)
	}

	err := extractTarGz(src, dst)
	if err == nil {
		t.Fatal("extractTarGz accepted a traversing member")
	}
	if !strings.Contains(err.Error(), "escapes extract dir") {
		t.Errorf("error = %v, want an escape complaint", err)
	}
	// Walk the whole parent, not just the expected landing spot: a
	// traversal that lands somewhere unanticipated is still a traversal,
	// and checking one path would miss it.
	if found := findFile(t, parent, "pwned.txt"); found != "" {
		t.Errorf("traversing member was written to %s", found)
	}
}

func TestExtractZipRefusesTraversal(t *testing.T) {
	src := zipOf(t, []tarEntry{
		{name: "wrapper/../../pwned.txt", body: "owned"},
	})
	parent := t.TempDir()
	dst := filepath.Join(parent, "extract")
	if err := os.MkdirAll(dst, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := extractZip(src, dst); err == nil {
		t.Fatal("extractZip accepted a traversing member")
	}
	if found := findFile(t, parent, "pwned.txt"); found != "" {
		t.Errorf("traversing member was written to %s", found)
	}
}

// TestExtractZipWritesFilesAndStripsWrapper mirrors the tar case. Both
// formats are reachable in production — Windows assets ship as .zip — and
// they are separate implementations of the same contract, so testing one
// says nothing about the other.
func TestExtractZipWritesFilesAndStripsWrapper(t *testing.T) {
	src := zipOf(t, []tarEntry{
		{name: "inferd-v0.6.0-windows-amd64/inferd.exe", body: "MZ"},
		{name: "inferd-v0.6.0-windows-amd64/backends/ggml.dll", body: "DLL"},
	})
	dst := t.TempDir()
	if err := extractZip(src, dst); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(dst, "inferd.exe")); err != nil {
		t.Errorf("wrapper not stripped: %v", err)
	} else if string(body) != "MZ" {
		t.Errorf("inferd.exe body = %q", body)
	}
	if _, err := os.Stat(filepath.Join(dst, "backends", "ggml.dll")); err != nil {
		t.Errorf("nested member missing: %v", err)
	}
}

// TestExtractTarGzSkipsBareTopLevelMembers: stripLeadingComponent returns
// "" for a member with no wrapper, and the loop must `continue` rather than
// join "" onto dst (which would resolve to dst itself and, for a regular
// file, try to truncate the directory).
func TestExtractTarGzSkipsBareTopLevelMembers(t *testing.T) {
	src := tarGz(t, []tarEntry{
		{name: "LICENSE", body: "MIT"},
		{name: "wrapper/inferd", body: "bin"},
	})
	dst := t.TempDir()
	if err := extractTarGz(src, dst); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "LICENSE")); err == nil {
		t.Error("bare top-level member was extracted; expected it to be skipped")
	}
	if _, err := os.Stat(filepath.Join(dst, "inferd")); err != nil {
		t.Errorf("the real member should still have been extracted: %v", err)
	}
}

// TestExtractTarGzRejectsCorruptInput: a non-gzip file must produce an
// error, not a panic. Reachable in production whenever a download is
// truncated or a mirror serves an HTML error page with a 200.
func TestExtractTarGzRejectsCorruptInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.tar.gz")
	if err := os.WriteFile(path, []byte("<html>404</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(path, t.TempDir()); err == nil {
		t.Error("expected an error for a non-gzip file")
	}
}

func TestExtractTarGzMissingFile(t *testing.T) {
	if err := extractTarGz(filepath.Join(t.TempDir(), "nope.tar.gz"), t.TempDir()); err == nil {
		t.Error("expected an error for a missing archive")
	}
}

func TestExtractZipRejectsCorruptInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(path, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(path, t.TempDir()); err == nil {
		t.Error("expected an error for a non-zip file")
	}
}

// TestExtractTarGzPreservesExecutableBit: the extracted inferd binary has
// to be runnable. extractTarGz masks the header mode with 0o777, so the
// bit should survive. Unix only — Windows has no execute bit and reports
// 0o666 regardless.
func TestExtractTarGzPreservesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no execute bit on Windows")
	}
	path := filepath.Join(t.TempDir(), "x.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "wrapper/inferd", Typeflag: tar.TypeReg, Mode: 0o755, Size: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("bin")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	if err := extractTarGz(path, dst); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dst, "inferd"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("extracted mode %v has no owner-execute bit; the daemon wouldn't run", fi.Mode().Perm())
	}
}

// findFile walks root looking for a file with the given base name,
// returning its path or "". Used to prove a traversal payload didn't land
// *anywhere*, rather than merely not landing where the test guessed.
func findFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable dir is not the thing under test
		}
		if !d.IsDir() && filepath.Base(p) == name {
			found = p
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestSHA256OfFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Known SHA-256 of "abc" — a literal, not a value recomputed with the
	// same library the function uses, which would pass even if the wrong
	// algorithm were wired in.
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got, err := sha256OfFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sha256OfFile = %s, want %s", got, want)
	}
}

func TestSHA256OfFileMissing(t *testing.T) {
	if _, err := sha256OfFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing file")
	}
}
