package pdfocr

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func pythonForTest() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// makePNG builds a w×h opaque-gray PNG and returns its bytes.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{128, 128, 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestPixelCapArithmetic locks the resource-bound constants so a careless
// edit (e.g. dropping the /3, or bumping the frame cap) fails fast. The
// decoded RGB of a maxPixels-sized page must fit the inferd frame cap.
func TestPixelCapArithmetic(t *testing.T) {
	if maxPixels*3 > 64<<20 {
		t.Errorf("maxPixels*3 (%d) exceeds the 64 MiB frame cap; a max-size page can't be sent", maxPixels*3)
	}
	if maxPNGBytes <= 0 {
		t.Error("maxPNGBytes must be positive")
	}
}

// TestDecodeConfigBoundsHeaderOnly confirms image.DecodeConfig reads
// dimensions from a PNG header cheaply — the mechanism renderPageRGB
// relies on to reject a decompression bomb before a full decode. A
// small PNG reports its true dimensions.
func TestDecodeConfigBoundsHeaderOnly(t *testing.T) {
	p := makePNG(t, 13, 21)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 13 || cfg.Height != 21 {
		t.Errorf("DecodeConfig dims = %dx%d, want 13x21", cfg.Width, cfg.Height)
	}
	if int64(cfg.Width)*int64(cfg.Height) > int64(maxPixels) {
		t.Error("a 13x21 page should be well under the pixel cap")
	}
}

// TestTranscribeRejectsBadOptions: the exported guards return errors
// (the caller turns these into the low-value passthrough) and never
// panic.
func TestTranscribeRejectsBadOptions(t *testing.T) {
	if _, err := Transcribe(context.Background(), nil, []byte("x"), Options{PageCount: 1}); err == nil {
		t.Error("expected error when ProcessorDir empty")
	}
	if _, err := Transcribe(context.Background(), nil, []byte("x"), Options{ProcessorDir: "d", PageCount: 0}); err == nil {
		t.Error("expected error when PageCount < 1")
	}
}

// TestPageCountBadProcessor: PageCount against a nonexistent processor
// dir fails cleanly (fail-open signal), never panics.
func TestPageCountBadProcessor(t *testing.T) {
	dir := t.TempDir()
	if _, err := PageCount(context.Background(), pythonForTest(), filepath.Join(dir, "missing"), []byte("%PDF-1.4")); err == nil {
		t.Error("expected error for missing processor dir")
	}
}

// TestTranscribeFailsOpenWhenNoPageRenders covers the whole-document
// failure path, which is the one ADR 0006 depends on: every page fails to
// rasterize, so Transcribe must return an error (the caller then keeps the
// original low-value output) rather than a document of placeholder lines
// that looks like a successful transcription.
//
// The client is nil on purpose. renderPageRGB fails before ocrPage ever
// touches it, so this also pins that ordering: if a future edit dialled
// inferd before rasterizing, this test would panic instead of failing, and
// a panic in a hook is the one outcome invariant #2 forbids outright.
func TestTranscribeFailsOpenWhenNoPageRenders(t *testing.T) {
	dir := t.TempDir()
	noPython := filepath.Join(dir, "definitely-not-python")
	out, err := Transcribe(context.Background(), nil, []byte("%PDF-1.4"), Options{
		ProcessorDir: dir,
		PageCount:    3,
		Python:       noPython,
	})
	if err == nil {
		t.Fatalf("Transcribe reported success with no page transcribed; got %q", out)
	}
	if out != "" {
		t.Errorf("Transcribe returned %q alongside an error; the caller may use it", out)
	}
	if !strings.Contains(err.Error(), "no page produced a transcription") {
		t.Errorf("error = %v, want the no-transcription complaint", err)
	}
}

// TestTranscribeHonoursCancellation: a cancelled context must end the run
// rather than grinding through every page. Reachable because the per-page
// render checks the context.
func TestTranscribeHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Transcribe(ctx, nil, []byte("%PDF-1.4"), Options{
		ProcessorDir: t.TempDir(),
		PageCount:    2,
	}); err == nil {
		t.Error("Transcribe ran to completion under a cancelled context")
	}
}

// TestTranscribeStopsRenderingAtPageCap: MaxPages is a cost control — the
// comment on it says a runaway 500-page scan would otherwise hang the read
// — so the assertion that matters is that it stops *doing the work*, not
// that the summary line says the right number. Counted by tallying stub
// subprocess launches.
//
// Uses the render stub so the pages fail after being attempted; the
// resulting error is expected and not what's under test here.
//
// Both the capped and uncapped runs are measured against the same
// 9-page document. Asserting only "3 renders" would leave open whether the
// clamp did anything or the loop merely happened to stop; the uncapped
// control run has to reach 9 for the capped number to mean something.
func TestTranscribeStopsRenderingAtPageCap(t *testing.T) {
	renders := func(t *testing.T, pageCount, maxPages int) int {
		t.Helper()
		tally := filepath.Join(t.TempDir(), "calls")
		python, dir := stubProcessor(t, "empty")
		t.Setenv(helperTally, tally)

		_, _ = Transcribe(context.Background(), nil, []byte("%PDF-1.4"), Options{
			ProcessorDir: dir,
			PageCount:    pageCount,
			MaxPages:     maxPages,
			Python:       python,
		})
		body, err := os.ReadFile(tally)
		if err != nil {
			t.Fatalf("stub was never invoked: %v", err)
		}
		return len(body)
	}

	if got := renders(t, 9, 3); got != 3 {
		t.Errorf("with MaxPages=3, rendered %d pages, want 3 — the cap did not bound the work", got)
	}
	// MaxPages 0 means no cap; this is the control that proves the number
	// above is the cap's doing.
	if got := renders(t, 9, 0); got != 9 {
		t.Errorf("with no cap, rendered %d pages, want all 9", got)
	}
}
