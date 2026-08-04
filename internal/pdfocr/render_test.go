package pdfocr

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Tests for the rasterization half of the package: PageCount and
// renderPageRGB, both previously at 0–64% coverage because they shell out
// to the pdf-to-md processor's run.py.
//
// They are reachable without Python because the interpreter is a
// parameter, not a constant: pointing it at this test binary and
// switching on an env var gives a stub subprocess whose output we
// control byte for byte. That matters more than the coverage number —
// renderPageRGB's bounds are the guard against a malicious PDF
// rasterizing to a page big enough to OOM the hook, and a guard with no
// test is a guard nobody has ever seen fire.
//
// The trick works because renderPageRGB passes the run.py path as the
// first argument. Go's testing flag parser stops at the first non-flag
// argument, so the render flags after it are never parsed as test flags.

const (
	helperEnv   = "PDFOCR_TEST_HELPER_MODE"
	helperWEnv  = "PDFOCR_TEST_HELPER_W"
	helperHEnv  = "PDFOCR_TEST_HELPER_H"
	helperTally = "PDFOCR_TEST_HELPER_TALLY"
)

// TestMain doubles as the stub subprocess. In helper mode it writes the
// requested output and exits without running any test.
func TestMain(m *testing.M) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		os.Exit(m.Run())
	}
	os.Exit(runHelper(mode))
}

func runHelper(mode string) int {
	// Drain stdin first. The parent writes the PDF through a pipe, and a
	// child that never reads would leave the parent's copy goroutine
	// blocked — which cmd.Wait then waits on. Real run.py reads stdin, so
	// the stub does too.
	pdf, _ := readAllStdin()

	// Record the invocation when the parent asked for a tally. Append mode
	// with one byte per call, so the parent can count renders without
	// parsing anything.
	if tally := os.Getenv(helperTally); tally != "" {
		if f, err := os.OpenFile(tally, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.WriteString("x")
			_ = f.Close()
		}
	}

	switch mode {
	case "png":
		w := envInt(helperWEnv, 4)
		h := envInt(helperHEnv, 3)
		_, _ = os.Stdout.Write(grayPNG(w, h))
	case "png-header-only":
		// A valid PNG header advertising dimensions we never allocate.
		// This is the decompression-bomb shape: tiny on the wire, huge
		// on decode.
		_, _ = os.Stdout.Write(pngHeaderOnly(envInt(helperWEnv, 1), envInt(helperHEnv, 1)))
	case "garbage":
		_, _ = os.Stdout.WriteString("<html>not a png</html>")
	case "empty":
		// exit 0 with no output
	case "fail":
		_, _ = os.Stderr.WriteString("render failed: no such page")
		return 3
	case "count":
		// Echo the stdin length, proving the PDF bytes actually reach the
		// subprocess rather than the call merely succeeding.
		_, _ = os.Stdout.WriteString(strconv.Itoa(len(pdf)) + "\n")
	case "count-bad":
		_, _ = os.Stdout.WriteString("seventeen\n")
	case "count-zero":
		_, _ = os.Stdout.WriteString("0\n")
	default:
		_, _ = os.Stderr.WriteString("unknown helper mode " + mode)
		return 2
	}
	return 0
}

func readAllStdin() ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(os.Stdin)
	return buf.Bytes(), err
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

// pngHeaderOnly builds a PNG signature plus a well-formed IHDR chunk and
// nothing else. image.DecodeConfig reads dimensions from IHDR alone, so
// this is enough to exercise the pre-decode pixel-cap check with a
// 40-byte file — the point of doing the check on the header.
func pngHeaderOnly(w, h int) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 0, 13)
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(w))
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(h))
	ihdr = append(ihdr, 8, 2, 0, 0, 0) // 8-bit, truecolor RGB, no interlace
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

// grayPNG encodes a real w×h PNG whose every pixel is 0x808080, so the
// RGB conversion can be checked byte for byte. Separate from makePNG
// because that one takes a *testing.T, which the helper process has not
// got — in helper mode no test is running.
func grayPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{0x80, 0x80, 0x80, 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil // helper process: the parent's decode will fail and report
	}
	return buf.Bytes()
}

// helperPython returns this test binary's path, to be passed where the
// code expects a Python interpreter.
func helperPython(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

// stubProcessor arms helper mode for the child (inherited via the
// environment, since the code under test doesn't set cmd.Env) and
// returns a processor dir. The dir need not contain a run.py: the stub
// ignores argv[1].
func stubProcessor(t *testing.T, mode string) (python, procDir string) {
	t.Helper()
	t.Setenv(helperEnv, mode)
	return helperPython(t), t.TempDir()
}

func TestRenderPageRGBConvertsPNGToRGB(t *testing.T) {
	python, dir := stubProcessor(t, "png")
	t.Setenv(helperWEnv, "4")
	t.Setenv(helperHEnv, "3")

	rgb, w, h, err := renderPageRGB(context.Background(), python, dir, []byte("%PDF-1.4"), 1)
	if err != nil {
		t.Fatalf("renderPageRGB: %v", err)
	}
	if w != 4 || h != 3 {
		t.Errorf("dims = %dx%d, want 4x3", w, h)
	}
	// Interleaved, no alpha: exactly 3 bytes per pixel is what inferd's
	// attachment contract requires (ADR 0016). An extra channel here
	// would misalign every row on the daemon side.
	if len(rgb) != 4*3*3 {
		t.Fatalf("len(rgb) = %d, want %d", len(rgb), 4*3*3)
	}
	for i, got := range rgb {
		if got != 0x80 {
			t.Fatalf("rgb[%d] = %#x, want 0x80 (gray page)", i, got)
			break
		}
	}
}

// TestRenderPageRGBRejectsOversizePixelCount is the decompression-bomb
// guard. The stub advertises a canvas past maxPixels in a 40-byte file;
// the check must fire on the header, before any pixel buffer is
// allocated. If it ever regresses, the failure mode is not a wrong
// answer but a dead machine.
func TestRenderPageRGBRejectsOversizePixelCount(t *testing.T) {
	python, dir := stubProcessor(t, "png-header-only")
	// 30000×30000 = 900M pixels, ~2.7 GB decoded as RGB.
	t.Setenv(helperWEnv, "30000")
	t.Setenv(helperHEnv, "30000")

	_, _, _, err := renderPageRGB(context.Background(), python, dir, []byte("%PDF"), 1)
	if err == nil {
		t.Fatal("renderPageRGB accepted a page past the pixel cap")
	}
	if !strings.Contains(err.Error(), "pixel cap") {
		t.Errorf("error = %v, want the pixel-cap refusal", err)
	}
}

// TestRenderPageRGBRejectsZeroDimensions: a 0-width page must be
// refused rather than yielding an empty attachment the daemon would
// reject less clearly.
func TestRenderPageRGBRejectsZeroDimensions(t *testing.T) {
	python, dir := stubProcessor(t, "png-header-only")
	t.Setenv(helperWEnv, "0")
	t.Setenv(helperHEnv, "10")

	_, _, _, err := renderPageRGB(context.Background(), python, dir, []byte("%PDF"), 1)
	if err == nil {
		t.Fatal("renderPageRGB accepted a zero-width page")
	}
}

func TestRenderPageRGBRejectsNonPNG(t *testing.T) {
	python, dir := stubProcessor(t, "garbage")
	_, _, _, err := renderPageRGB(context.Background(), python, dir, []byte("%PDF"), 1)
	if err == nil {
		t.Fatal("renderPageRGB accepted non-PNG output")
	}
	if !strings.Contains(err.Error(), "PNG header") {
		t.Errorf("error = %v, want a header-decode complaint", err)
	}
}

func TestRenderPageRGBRejectsEmptyOutput(t *testing.T) {
	python, dir := stubProcessor(t, "empty")
	if _, _, _, err := renderPageRGB(context.Background(), python, dir, []byte("%PDF"), 1); err == nil {
		t.Fatal("renderPageRGB accepted an empty render")
	}
}

// TestRenderPageRGBSurfacesSubprocessStderr: when run.py exits non-zero
// its diagnostics must reach the error, because that string is all the
// user gets when a scanned PDF silently declines to OCR.
func TestRenderPageRGBSurfacesSubprocessStderr(t *testing.T) {
	python, dir := stubProcessor(t, "fail")
	_, _, _, err := renderPageRGB(context.Background(), python, dir, []byte("%PDF"), 1)
	if err == nil {
		t.Fatal("renderPageRGB ignored a non-zero exit")
	}
	if !strings.Contains(err.Error(), "no such page") {
		t.Errorf("error = %v, want the subprocess stderr included", err)
	}
}

// TestRenderPageRGBMissingInterpreter covers the fail-open shape that
// actually happens in the field: no python3 on PATH (v0.11.3 made Python
// optional, so this is now the common case for anyone who never installed
// it). It must be an error, not a panic or a hang.
func TestRenderPageRGBMissingInterpreter(t *testing.T) {
	dir := t.TempDir()
	nope := filepath.Join(dir, "definitely-not-python")
	_, _, _, err := renderPageRGB(context.Background(), nope, dir, []byte("%PDF"), 1)
	if err == nil {
		t.Fatal("expected an error for a missing interpreter")
	}
}

// TestRenderPageRGBHonoursContextCancellation: the render is bounded by
// the caller's context. Without this the hook has no way to stop a
// wedged rasterizer.
func TestRenderPageRGBHonoursContextCancellation(t *testing.T) {
	python, dir := stubProcessor(t, "png")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := renderPageRGB(ctx, python, dir, []byte("%PDF"), 1); err == nil {
		t.Error("renderPageRGB ran with an already-cancelled context")
	}
}

func TestPageCountReadsStdinAndParses(t *testing.T) {
	python, dir := stubProcessor(t, "count")
	pdf := []byte("%PDF-1.7") // 8 bytes; the stub echoes the length it read
	n, err := PageCount(context.Background(), python, dir, pdf)
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if n != len(pdf) {
		t.Errorf("PageCount = %d, want %d — the PDF bytes did not reach the subprocess intact",
			n, len(pdf))
	}
}

func TestPageCountRejectsUnparseableOutput(t *testing.T) {
	python, dir := stubProcessor(t, "count-bad")
	if _, err := PageCount(context.Background(), python, dir, []byte("%PDF")); err == nil {
		t.Error("PageCount accepted non-numeric output")
	}
}

// TestPageCountRejectsZero: a zero page count must be an error, because
// Transcribe's own guard rejects PageCount < 1 and a silent 0 would turn
// into a confusing second failure one layer up.
func TestPageCountRejectsZero(t *testing.T) {
	python, dir := stubProcessor(t, "count-zero")
	if _, err := PageCount(context.Background(), python, dir, []byte("%PDF")); err == nil {
		t.Error("PageCount accepted 0 pages")
	}
}

func TestPageCountSurfacesSubprocessFailure(t *testing.T) {
	python, dir := stubProcessor(t, "fail")
	_, err := PageCount(context.Background(), python, dir, []byte("%PDF"))
	if err == nil {
		t.Fatal("PageCount ignored a non-zero exit")
	}
	if !strings.Contains(err.Error(), "no such page") {
		t.Errorf("error = %v, want the subprocess stderr included", err)
	}
}

// TestPageCountDefaultsInterpreter: an empty python argument must fall
// back to python3 rather than exec'ing "". The call fails (no stub), but
// the error must name the interpreter, proving the default was applied.
func TestPageCountDefaultsInterpreter(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // guarantee python3 is not resolvable
	_, err := PageCount(context.Background(), "", t.TempDir(), []byte("%PDF"))
	if err == nil {
		t.Fatal("expected an error with no interpreter available")
	}
	if !strings.Contains(err.Error(), "python3") {
		t.Errorf("error = %v, want it to name the python3 default", err)
	}
}
