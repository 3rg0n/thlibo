package casecmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate points $HOME and the cases root at temp dirs. Both matter:
// casesDir falls back to $THLIBO_CASES_DIR and then to
// ~/.thlibo/cases, and the pipeline reads ~/.thlibo/processors, so an
// un-isolated test would write into the developer's real case history
// and could be shadowed by a user processor on disk.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("THLIBO_PROCESSORS_DIR", t.TempDir())
	cases := t.TempDir()
	t.Setenv("THLIBO_CASES_DIR", cases)
	return cases
}

// writeLog drops a log-shaped file of roughly n lines and returns its
// path. Log-shaped so it is plausible input; size is what matters for
// crossing the routing floor.
func writeLog(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("2026-08-05T12:00:00Z INFO worker processed batch item with some padding text\n")
	}
	p := filepath.Join(t.TempDir(), "big.log")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The happy path: a case dir is created, its path is the only thing on
// stdout (the Read hook parses stdout to find it), and the three
// artifacts are on disk.
func TestCaseCreatesCaseDir(t *testing.T) {
	cases := isolate(t)
	src := writeLog(t, 200)

	code := Run([]string{"--quiet", src})
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK(%d)", code, ExitOK)
	}

	entries, err := os.ReadDir(cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("cases root holds %d entries, want exactly 1", len(entries))
	}
	dir := filepath.Join(cases, entries[0].Name())
	for _, name := range []string{"compressed.log", "summary.md", "meta.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing case artifact %s: %v", name, err)
		}
	}
}

// A missing source is exit 3, not a generic failure. The Read hook keys
// on the specific code, so collapsing 3 into 5 would change hook
// behaviour without changing any output the tests inspect.
func TestCaseMissingSourceExits3(t *testing.T) {
	isolate(t)
	absent := filepath.Join(t.TempDir(), "not-here.log")

	if code := Run([]string{"--quiet", absent}); code != ExitSourceMissing {
		t.Fatalf("exit = %d, want ExitSourceMissing(%d)", code, ExitSourceMissing)
	}
}

// A directory is not a regular file: exit 4, distinct from "missing".
func TestCaseDirectorySourceExits4(t *testing.T) {
	isolate(t)
	dir := t.TempDir()

	if code := Run([]string{"--quiet", dir}); code != ExitNotRegular {
		t.Fatalf("exit = %d, want ExitNotRegular(%d)", code, ExitNotRegular)
	}
}

// Wrong argument count is a usage error. Zero args is the interesting
// case: without the NArg check it would fall through to Create("") and
// report a confusing source-missing error instead.
func TestCaseWrongArgCountExits2(t *testing.T) {
	isolate(t)
	src := writeLog(t, 20)

	for _, argv := range [][]string{
		{"--quiet"},
		{"--quiet", src, src},
	} {
		if code := Run(argv); code != ExitUsage {
			t.Errorf("Run(%q) = %d, want ExitUsage(%d)", argv, code, ExitUsage)
		}
	}
}

// An unknown flag is also exit 2, and must not create a case.
func TestCaseUnknownFlagExits2(t *testing.T) {
	cases := isolate(t)

	if code := Run([]string{"--nope", "x.log"}); code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage(%d)", code, ExitUsage)
	}
	entries, _ := os.ReadDir(cases)
	if len(entries) != 0 {
		t.Errorf("a usage error created %d case dirs; want 0", len(entries))
	}
}

// --prune is a terminal branch: it prunes and returns without touching
// argv's positional, so it must not require a source file. Old dirs go,
// recent dirs stay.
func TestCasePruneRemovesOnlyOldCases(t *testing.T) {
	cases := isolate(t)

	old := filepath.Join(cases, "20200101T000000Z-deadbeef")
	recent := filepath.Join(cases, "20990101T000000Z-cafebabe")
	for _, d := range []string{old, recent} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Prune keys on mtime, not the directory name.
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	if code := Run([]string{"--prune", "24h"}); code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK(%d)", code, ExitOK)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old case dir survived --prune")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent case dir was pruned: %v", err)
	}
}

// --prune 0 is not a prune request: a zero duration falls through to the
// normal path, so `thlibo case --prune 0` with no file is a usage error
// rather than a silent no-op success. Pins the `pruneAge > 0` guard.
func TestCasePruneZeroIsNotAPruneRequest(t *testing.T) {
	isolate(t)

	if code := Run([]string{"--prune", "0"}); code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage(%d) — --prune 0 must not be treated as a prune", code, ExitUsage)
	}
}

// --dir wins over $THLIBO_CASES_DIR. The flag is what the Read hook
// passes, so a precedence inversion would scatter cases into whichever
// dir the user's environment named.
func TestCaseDirFlagBeatsEnv(t *testing.T) {
	envCases := isolate(t)
	flagCases := t.TempDir()
	src := writeLog(t, 200)

	if code := Run([]string{"--quiet", "--dir", flagCases, src}); code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK(%d)", code, ExitOK)
	}

	inFlag, _ := os.ReadDir(flagCases)
	inEnv, _ := os.ReadDir(envCases)
	if len(inFlag) != 1 {
		t.Errorf("--dir received %d cases, want 1", len(inFlag))
	}
	if len(inEnv) != 0 {
		t.Errorf("$THLIBO_CASES_DIR received %d cases despite --dir, want 0", len(inEnv))
	}
}

// A PDF with no extractable text is the #31 path: the case is still
// written to disk for forensics, but the exit code is 6 so the Read
// PreToolUse hook treats it as no-match and lets Claude's native reader
// handle the original.
//
// OCR would normally clear the flag, but it needs a reachable
// vision-capable daemon; with none, the placeholder and the LowValue
// flag survive — which is the fail-open behaviour being pinned here.
func TestCaseLowValuePDFExits6(t *testing.T) {
	cases := isolate(t)
	src := filepath.Join(t.TempDir(), "scanned.pdf")
	pdfBytes := textlessPDF()
	// Guard the fixture, not just the behaviour: below the routing floor
	// this test would short-circuit to ExitOK and silently stop testing
	// the #31 path.
	if len(pdfBytes) < 2000 {
		t.Fatalf("fixture is %d bytes; must exceed the 2000-byte routing floor or pdf-filter never runs", len(pdfBytes))
	}
	if err := os.WriteFile(src, pdfBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	code := Run([]string{"--quiet", src})
	if code != ExitLowValue {
		t.Fatalf("exit = %d, want ExitLowValue(%d)", code, ExitLowValue)
	}
	// The case dir must still exist: the exit code says "don't use
	// this", not "nothing happened".
	entries, err := os.ReadDir(cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("low-value run left %d case dirs, want 1 (kept for forensics)", len(entries))
	}
}

// textlessPDF builds a single-page PDF whose content stream draws
// rectangles and no text, so pdf-filter finds nothing to extract and
// emits its low-value sentinel. Synthetic on purpose — the repo does not
// vendor real PDFs.
//
// The rectangle is repeated to push the file past
// middleware.MinBytesForRouting (2000). Under that floor the pipeline
// short-circuits before pdf-filter ever runs, the sentinel is never
// emitted, and the test passes as ExitOK while proving nothing.
func textlessPDF() []byte {
	content := "0 0 0 rg\n" + strings.Repeat("100 100 200 200 re f\n", 120)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>",
		"<< /Length " + itoa(len(content)) + " >>\nstream\n" + content + "endstream",
	}

	var b strings.Builder
	b.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = b.Len()
		b.WriteString(itoa(i+1) + " 0 obj\n" + body + "\nendobj\n")
	}
	xref := b.Len()
	b.WriteString("xref\n0 " + itoa(len(objs)+1) + "\n")
	b.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		b.WriteString(pad10(offsets[i]) + " 00000 n \n")
	}
	b.WriteString("trailer\n<< /Size " + itoa(len(objs)+1) + " /Root 1 0 R >>\nstartxref\n" +
		itoa(xref) + "\n%%EOF\n")
	return []byte(b.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// pad10 renders n as the 10-digit, zero-padded offset an xref entry
// requires. A short field shifts every subsequent byte and the parser
// rejects the file.
func pad10(n int) string {
	s := itoa(n)
	return strings.Repeat("0", 10-len(s)) + s
}

// pdfOCRFunc is constructed on every `case` run, so it must never panic
// or dial anything at construction time — only when casefile invokes it
// on a low-value PDF. Calling it with input it cannot handle must return
// an error rather than succeeding, since casefile keys "keep the #31
// placeholder" on a non-nil error; a nil error with empty text would
// blank the case's compressed.log.
//
// Platform-independent because every reachable failure here (no
// pdf-to-md/run.py under the isolated processors dir, no python3 on
// PATH, non-PDF bytes) is required to produce an error. The test asserts
// the contract, not which of the three fired.
func TestPDFOCRFuncFailsClosedOnNonPDF(t *testing.T) {
	isolate(t)

	fn := pdfOCRFunc()
	if fn == nil {
		t.Fatal("pdfOCRFunc returned nil; casefile would skip OCR silently")
	}

	md, err := fn(t.Context(), []byte("this is not a pdf"))
	if err == nil {
		t.Fatalf("OCR reported success on non-PDF input (md=%q); casefile would replace the placeholder with garbage", md)
	}
	if md != "" {
		t.Errorf("OCR returned content alongside an error: %q", md)
	}
}
