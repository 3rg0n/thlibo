package casecmd

import (
	"context"
	"path/filepath"

	"github.com/3rg0n/thlibo/internal/inferd"
	"github.com/3rg0n/thlibo/internal/install"
	"github.com/3rg0n/thlibo/internal/pdfocr"
)

// maxOCRPages caps how many pages of a scanned PDF we OCR. Vision is
// slow + token-heavy; a runaway 500-page scan would otherwise hang the
// read. Pages beyond the cap are noted in the output (pdfocr appends a
// marker). Tunable later via config if needed.
const maxOCRPages = 25

// pdfOCRFunc returns the OCR hook passed to casefile.Create. It is
// invoked only when a PDF came back low-value (scanned). Everything is
// fail-open: a non-PDF, an unreachable/vision-less daemon, or any error
// returns ("", err) so casefile keeps the #31 placeholder passthrough.
func pdfOCRFunc() func(ctx context.Context, pdfBytes []byte) (string, error) {
	return func(ctx context.Context, pdfBytes []byte) (string, error) {
		procDir := filepath.Join(install.DefaultProcessorsDir(), "pdf-to-md")
		client := &inferd.Client{Address: inferd.DefaultGenerationAddress()}

		n, err := pdfocr.PageCount(ctx, "python3", procDir, pdfBytes)
		if err != nil {
			return "", err
		}
		return pdfocr.Transcribe(ctx, client, pdfBytes, pdfocr.Options{
			ProcessorDir: procDir,
			PageCount:    n,
			MaxPages:     maxOCRPages,
		})
	}
}
