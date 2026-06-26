// Package pdfocr performs vision OCR on scanned (image-only) PDF pages
// via inferd's Gemma vision path, dispatched Go-side (ADR 0009).
//
// The PDF processor (pdf-to-md) is a stdin/stdout text script: it can't
// carry raw RGB or speak inferd's binary wire. So when a PDF comes back
// as low-value (every page a scanned-image placeholder), the Go side
// takes over here:
//
//  1. Rasterize each page to PNG via the processor's `--render-page`
//     mode (reuses the processor's existing PDF deps; one dependency
//     set, kept out of Go).
//  2. Decode PNG → raw interleaved RGB (the daemon links no image codec;
//     the consumer decodes — inferd ADR 0016).
//  3. Send each page as an inferd image attachment with an OCR prompt.
//  4. Assemble the per-page transcriptions into Markdown.
//
// Fail-open throughout (ADR 0006): a page that can't be rendered or
// whose vision call errors falls back to a placeholder line for that
// page; a wholly-failed document returns an error and the caller keeps
// the original low-value behaviour. This package never panics on bad
// input and never blocks the AI client.
package pdfocr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder for image.Decode/DecodeConfig
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/3rg0n/thlibo/internal/inferd"
)

// ocrPrompt instructs the vision model to transcribe a page. Kept as a
// compile-time constant (no untrusted interpolation) — the only
// variable input is the image bytes, which ride in the attachment.
const ocrPrompt = "Transcribe all text visible in this scanned document page, " +
	"preserving structure as GitHub-flavored Markdown (headings, lists, tables). " +
	"Output only the transcription, no commentary."

// Tuning. maxDPI balances OCR fidelity against image-token cost; the
// 64 MiB frame cap bounds the upper end regardless.
const (
	renderDPI       = 200
	maxOCRMaxTokens = 2048
)

// Options configures a Transcribe run.
type Options struct {
	// ProcessorDir is the on-disk pdf-to-md processor directory
	// (…/.thlibo/processors/pdf-to-md) whose run.py provides
	// --render-page. Required.
	ProcessorDir string
	// PageCount is the number of pages to OCR (1-based, 1..PageCount).
	PageCount int
	// Python is the interpreter to invoke (default "python3").
	Python string
	// MaxPages caps how many pages are OCR'd; 0 = no cap. A guard
	// against a huge scanned doc costing unbounded time/tokens; the
	// caller logs when pages are dropped.
	MaxPages int
}

// Transcribe rasterizes and OCRs each page of pdfBytes, returning
// assembled Markdown. client is thlibo's inferd client; the caller is
// responsible for having confirmed (or being willing to fail open on)
// vision capability. Returns an error only if NO page produced any
// transcription — a partial success returns the pages that worked plus
// placeholder lines for those that didn't.
func Transcribe(ctx context.Context, client *inferd.Client, pdfBytes []byte, opts Options) (string, error) {
	if opts.ProcessorDir == "" {
		return "", fmt.Errorf("pdfocr: ProcessorDir required")
	}
	if opts.PageCount < 1 {
		return "", fmt.Errorf("pdfocr: PageCount must be >= 1")
	}
	python := opts.Python
	if python == "" {
		python = "python3"
	}

	pages := opts.PageCount
	dropped := 0
	if opts.MaxPages > 0 && pages > opts.MaxPages {
		dropped = pages - opts.MaxPages
		pages = opts.MaxPages
	}

	var md strings.Builder
	if dropped > 0 {
		fmt.Fprintf(&md, "- pages: %d (OCR'd %d of %d; page cap %d)\n", opts.PageCount, pages, opts.PageCount, opts.MaxPages)
	} else {
		fmt.Fprintf(&md, "- pages: %d\n", opts.PageCount)
	}
	any := false
	for n := 1; n <= pages; n++ {
		fmt.Fprintf(&md, "\n## Page %d\n\n", n)
		text, err := ocrPage(ctx, client, python, opts.ProcessorDir, pdfBytes, n)
		if err != nil || strings.TrimSpace(text) == "" {
			// Per-page fail-open: keep the placeholder, keep going.
			md.WriteString("_[scanned page " + strconv.Itoa(n) + " — OCR produced no text]_\n")
			continue
		}
		any = true
		md.WriteString(strings.TrimSpace(text))
		md.WriteString("\n")
	}
	if dropped > 0 {
		fmt.Fprintf(&md, "\n_[%d further page(s) not OCR'd: page cap %d reached]_\n", dropped, opts.MaxPages)
	}
	if !any {
		return "", fmt.Errorf("pdfocr: no page produced a transcription")
	}
	return md.String(), nil
}

// PageCount invokes the processor's --page-count mode to count pages in
// pdfBytes. Returns an error the caller should treat as "can't OCR this"
// (fail open).
func PageCount(ctx context.Context, python, procDir string, pdfBytes []byte) (int, error) {
	if python == "" {
		python = "python3"
	}
	entry := filepath.Join(procDir, "run.py")
	// python is "python3"/installed interpreter, entry is the mirrored
	// processor path, the only flag is a const; no caller-controlled
	// argv, no shell.
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(ctx, python, entry, "--page-count") // #nosec G204
	cmd.Stdin = bytes.NewReader(pdfBytes)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("pdfocr: page-count: %w (%s)", err, strings.TrimSpace(errb.String()))
	}
	n, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("pdfocr: bad page count %q", strings.TrimSpace(out.String()))
	}
	return n, nil
}

// ocrPage rasterizes one page to RGB and sends it through the vision
// path, returning the model's transcription text.
func ocrPage(ctx context.Context, client *inferd.Client, python, procDir string, pdfBytes []byte, page int) (string, error) {
	rgb, w, h, err := renderPageRGB(ctx, python, procDir, pdfBytes, page)
	if err != nil {
		return "", err
	}
	maxTok := maxOCRMaxTokens
	temp := 0.0
	req := inferd.Request{
		ID: "pdfocr-p" + strconv.Itoa(page),
		Messages: []inferd.Message{{
			Role:      inferd.RoleUser,
			Content:   ocrPrompt,
			ImageRefs: []string{"page"},
		}},
		Attachments: []inferd.Attachment{{
			ID: "page", Width: w, Height: h, RGB: rgb,
		}},
		MaxTokens:   &maxTok,
		Temperature: &temp,
	}
	res, err := client.Post(ctx, req)
	if err != nil {
		return "", fmt.Errorf("pdfocr: vision call page %d: %w", page, err)
	}
	return res.Text, nil
}

// Resource bounds for rasterized pages. A decoded RGB page must fit the
// inferd frame cap (64 MiB); maxPixels derives from that. maxPNGBytes
// caps the *compressed* bytes we read from the render subprocess, so a
// decompression-bomb PNG (tiny on disk, enormous decoded) can't OOM us
// before we ever look at its dimensions.
const (
	maxPixels   = inferd.MaxFrameBytes / 3 // RGB is 3 bytes/px; ~22.3M px
	maxPNGBytes = 64 << 20                 // generous cap on compressed render output
)

// renderPageRGB invokes the processor's --render-page mode to get a PNG,
// then decodes it to interleaved RGB (width*height*3, no alpha). Every
// step is bounded so a malicious PDF cannot exhaust memory:
//   - the PNG bytes read from the subprocess are capped (maxPNGBytes);
//   - dimensions are read from the PNG header (DecodeConfig) and checked
//     against maxPixels BEFORE the full pixel decode allocates anything.
func renderPageRGB(ctx context.Context, python, procDir string, pdfBytes []byte, page int) ([]byte, uint32, uint32, error) {
	entry := filepath.Join(procDir, "run.py")
	// python + entry are thlibo-controlled (mirrored processor dir +
	// fixed interpreter); page/dpi are ints via strconv. No shell, no
	// caller-controlled argv.
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(ctx, python, entry, // #nosec G204
		"--render-page", strconv.Itoa(page), "--dpi", strconv.Itoa(renderDPI))
	cmd.Stdin = bytes.NewReader(pdfBytes)
	// Cap stdout: read at most maxPNGBytes+1 so we can detect overflow
	// without buffering an unbounded gigabyte PNG.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("pdfocr: render page %d: stdout pipe: %w", page, err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return nil, 0, 0, fmt.Errorf("pdfocr: render page %d: start: %w", page, err)
	}
	pngBytes, readErr := io.ReadAll(io.LimitReader(stdout, maxPNGBytes+1))
	// Always Wait to reap the process; combine with read/cap errors.
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, 0, 0, fmt.Errorf("pdfocr: render page %d: read: %w", page, readErr)
	}
	if waitErr != nil {
		return nil, 0, 0, fmt.Errorf("pdfocr: render page %d: %w (%s)", page, waitErr, strings.TrimSpace(errb.String()))
	}
	if len(pngBytes) > maxPNGBytes {
		return nil, 0, 0, fmt.Errorf("pdfocr: render page %d: PNG exceeds %d byte cap", page, maxPNGBytes)
	}

	// Header-only dimension check BEFORE decoding pixels — defeats a
	// decompression bomb (small PNG, huge canvas).
	cfg, _, err := image.DecodeConfig(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("pdfocr: decode page %d PNG header: %w", page, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, 0, 0, fmt.Errorf("pdfocr: page %d has empty dimensions", page)
	}
	if int64(cfg.Width)*int64(cfg.Height) > int64(maxPixels) {
		return nil, 0, 0, fmt.Errorf("pdfocr: page %d %dx%d exceeds %d-pixel cap", page, cfg.Width, cfg.Height, maxPixels)
	}

	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("pdfocr: decode page %d PNG: %w", page, err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || int64(w)*int64(h) > int64(maxPixels) {
		// Defensive: decoded bounds must agree with the header check.
		return nil, 0, 0, fmt.Errorf("pdfocr: page %d decoded dimensions %dx%d invalid", page, w, h)
	}
	rgb := make([]byte, 0, w*h*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// RGBA returns 16-bit-per-channel values in [0,0xffff];
			// >>8 then & 0xff yields the high byte, provably a byte.
			r, g, bl, _ := img.At(x, y).RGBA()
			rgb = append(rgb, byte((r>>8)&0xff), byte((g>>8)&0xff), byte((bl>>8)&0xff))
		}
	}
	// #nosec G115 -- w,h are each <= maxPixels (~22.3M) per the checks
	// above, so both fit uint32 with no overflow.
	return rgb, uint32(w), uint32(h), nil
}
