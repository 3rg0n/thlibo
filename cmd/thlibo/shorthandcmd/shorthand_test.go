package shorthandcmd

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// emitOriginal in stdout mode writes the raw bytes verbatim and
// returns ExitOK. The earlier behaviour returned ExitBackendDown
// with an empty stdout when the daemon was offline — wiring this
// into `thlibo shorthand --in-place foo.md` from a pre-commit
// hook silently truncated files. Test prevents regression.
func TestEmitOriginalStdoutWritesRawBytes(t *testing.T) {
	raw := "line one\nline two\n# A NEVER-touched directive.\n"

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	rc := emitOriginal("foo.md", raw, /*inPlace*/ false, /*noBackup*/ false, /*quiet*/ true)
	w.Close()
	if rc != ExitOK {
		t.Errorf("exit code = %d, want ExitOK", rc)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	if buf.String() != raw {
		t.Errorf("stdout = %q, want %q", buf.String(), raw)
	}
}

// emitOriginal in --in-place mode is a no-op: the file already
// holds the original bytes, so writing them again would just bump
// mtime for no value. Test asserts the file is left untouched and
// no .orig backup is created.
func TestEmitOriginalInPlaceIsNoop(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/foo.md"
	original := []byte("original content\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeStat, _ := os.Stat(target)

	rc := emitOriginal(target, string(original), /*inPlace*/ true, /*noBackup*/ false, /*quiet*/ true)
	if rc != ExitOK {
		t.Errorf("exit code = %d, want ExitOK", rc)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("file content drifted")
	}

	afterStat, _ := os.Stat(target)
	if !beforeStat.ModTime().Equal(afterStat.ModTime()) {
		t.Errorf("file was rewritten despite in-place no-op contract; mtime drifted from %v to %v",
			beforeStat.ModTime(), afterStat.ModTime())
	}

	// No .orig backup should exist either — there's no new content
	// to back up against.
	if _, err := os.Stat(target + ".orig"); err == nil {
		t.Errorf(".orig backup created despite in-place no-op contract")
	}
}
