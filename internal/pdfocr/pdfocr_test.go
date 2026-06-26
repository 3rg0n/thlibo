package pdfocr

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"runtime"
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
