package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Model describes one downloadable GGUF quantisation. Pinning URL +
// expected SHA per variant means a single supply-chain swap on
// HuggingFace can't silently change what `thlibo install` ships.
type Model struct {
	// Name is the stable identifier users pass to `thlibo pull`.
	Name string
	// URL is the direct-download endpoint. We resolve it exactly;
	// no `main` branch chasing, no latest-tag trickery.
	URL string
	// ExpectedSHA256 is the lowercase hex SHA-256 of the GGUF bytes
	// we verified against. Empty = skip verification (not recommended
	// in production; used only by the offline test server).
	ExpectedSHA256 string
	// Filename is the on-disk name this model gets written to
	// inside the models directory.
	Filename string
	// SizeBytes is an advisory size (for progress + disk-space
	// preflight). Zero = unknown.
	SizeBytes int64
}

// pinnedGemma4E4BQ4KM is the SHA-256 of
// bartowski/google_gemma-4-E4B-it-GGUF/google_gemma-4-E4B-it-Q4_K_M.gguf
// as downloaded on 2026-05-13 (5,405,168,384 bytes).
//
// Can be overridden at build time via -ldflags -X (see the CI
// release workflow) so a new release can pin a newer GGUF revision
// without a source-code change. The baked-in value is the current
// canonical hash so that `go build` from source produces a working
// binary without `--allow-unpinned` — no special flags needed.
var pinnedGemma4E4BQ4KM = "51865750adafd22de56994a343d5a887cc1a589b9bae41d62b748c8bd0ca9c76"

// DefaultModel is the CPU-default Gemma 4 E4B Q4_K_M quantisation
// per spec §Model. Pinned to a bartowski repack for reproducibility.
//
// ExpectedSHA256 is sourced from pinnedGemma4E4BQ4KM above; empty
// in a local dev build, set in release builds via -ldflags -X.
// `thlibo pull` refuses to download with an empty expected hash
// unless --allow-unpinned is passed.
var DefaultModel = Model{
	Name:           "gemma-4-e4b-q4_k_m",
	URL:            "https://huggingface.co/bartowski/google_gemma-4-E4B-it-GGUF/resolve/main/google_gemma-4-E4B-it-Q4_K_M.gguf",
	ExpectedSHA256: pinnedGemma4E4BQ4KM,
	Filename:       "gemma-4-e4b-q4_k_m.gguf",
	SizeBytes:      5_405_168_384, // exact from HF tree listing
}

// ModelsDir returns the directory GGUFs live in. Honors the
// THLIBO_MODELS_DIR override for tests and for operators who want
// the GGUFs on a different disk than the rest of ~/.thlibo/.
func ModelsDir() string {
	if d := os.Getenv("THLIBO_MODELS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "thlibo", "models")
	}
	return filepath.Join(home, ".thlibo", "models")
}

// PullOptions tunes the download behaviour. Zero values produce the
// standard production download.
type PullOptions struct {
	// Dir overrides ModelsDir().
	Dir string
	// Client overrides the HTTP client. Tests use a client bound to
	// a httptest.Server; production uses the default below.
	Client *http.Client
	// AllowUnpinned permits a download even when ExpectedSHA256 is
	// empty. Required for the v0.1 bootstrap flow; will go away
	// once the canonical hash is pinned in DefaultModel.
	AllowUnpinned bool
	// Progress is invoked periodically during the download with the
	// byte counts. Zero-value no-op if nil.
	Progress ProgressFunc
	// Resume enables HTTP Range continuation if a partial .part file
	// from a previous run is still on disk. Default: true.
	Resume bool
	// NoResume is the negation of Resume, exposed so callers that
	// build options from flags don't need to set both. Takes
	// precedence if both Resume and NoResume are set.
	NoResume bool
}

// ProgressFunc is called with (written, total) byte counts.
// `total` is -1 when the server didn't report a Content-Length.
type ProgressFunc func(written, total int64)

// defaultHTTPClient is tuned for long-lived large downloads: no
// global timeout on the client (we use the context instead),
// generous dialer/TLS timeouts for slow networks.
var defaultHTTPClient = &http.Client{
	Transport: &http.Transport{
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 10 * time.Second,
	},
	// No Timeout: a 2.5 GB download on a 5 Mbps line takes 70
	// minutes. Callers pass a context for cancellation instead.
}

// Pull downloads the given Model to opts.Dir (or ModelsDir()).
// Returns the absolute path to the downloaded file on success. If
// the file already exists and its SHA matches ExpectedSHA256, Pull
// returns immediately without re-downloading (idempotent).
func Pull(ctx context.Context, m Model, opts PullOptions) (string, error) {
	if err := validateModelURL(m.URL); err != nil {
		return "", err
	}
	if m.ExpectedSHA256 == "" && !opts.AllowUnpinned {
		return "", fmt.Errorf("install: model %q has no pinned SHA256; pass --allow-unpinned to download anyway", m.Name)
	}

	dir := opts.Dir
	if dir == "" {
		dir = ModelsDir()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("install: create models dir: %w", err)
	}

	final := filepath.Join(dir, m.Filename)

	// Idempotency: if the final file is already present and valid,
	// we're done.
	if ok, err := verifyIfPresent(final, m.ExpectedSHA256); err != nil {
		return "", err
	} else if ok {
		return final, nil
	}

	client := opts.Client
	if client == nil {
		client = defaultHTTPClient
	}

	partial := final + ".part"
	resume := opts.Resume && !opts.NoResume
	if !opts.Resume && !opts.NoResume {
		// Unspecified: default to resume-enabled.
		resume = true
	}

	if err := download(ctx, client, m.URL, partial, resume, opts.Progress); err != nil {
		return "", err
	}

	// Verify post-download.
	if m.ExpectedSHA256 != "" {
		if err := verifySHA(partial, m.ExpectedSHA256); err != nil {
			// Don't keep a corrupted file around.
			_ = os.Remove(partial)
			return "", err
		}
	}

	if err := os.Rename(partial, final); err != nil {
		return "", fmt.Errorf("install: finalise %s: %w", final, err)
	}
	return final, nil
}

// download streams url into dest. If resume is true and dest already
// exists, a HTTP Range request continues from the existing size.
func download(ctx context.Context, client *http.Client, rawURL, dest string, resume bool, progress ProgressFunc) error {
	var offset int64
	if resume {
		if fi, err := os.Stat(dest); err == nil {
			offset = fi.Size()
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("install: build request: %w", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("install: GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored our Range (or we weren't resuming); start
		// from scratch.
		offset = 0
	case http.StatusPartialContent:
		// Expected when resuming.
	case http.StatusRequestedRangeNotSatisfiable:
		// We asked for a range past the end: the partial file is
		// already the full size. Treat as "server says we're done
		// downloading"; verification will catch a truncated file.
		return nil
	default:
		return fmt.Errorf("install: GET %s: http %d", rawURL, resp.StatusCode)
	}

	total := int64(-1)
	if cl := resp.ContentLength; cl > 0 {
		total = cl + offset
	}

	// Write with O_APPEND when resuming, O_TRUNC when starting over.
	flag := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if offset == 0 {
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(dest, flag, 0o600) // #nosec G304 -- dest is constructed from opts.Dir + Model.Filename, both thlibo-owned config values
	if err != nil {
		return fmt.Errorf("install: open %s: %w", dest, err)
	}
	defer f.Close()

	var writer io.Writer = f
	if progress != nil {
		writer = &progressWriter{w: f, written: offset, total: total, fn: progress}
	}

	if _, err := io.Copy(writer, resp.Body); err != nil {
		return fmt.Errorf("install: stream %s: %w", dest, err)
	}
	return nil
}

// progressWriter decorates an io.Writer to call a ProgressFunc every
// ~64 KiB of data written. We don't rate-limit beyond that: callers
// who want a throttled UI can implement one in their own ProgressFunc.
type progressWriter struct {
	w       io.Writer
	written int64
	total   int64
	fn      ProgressFunc
	nextTick int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if n > 0 {
		p.written += int64(n)
		if p.written >= p.nextTick {
			p.fn(p.written, p.total)
			p.nextTick = p.written + 65536
		}
	}
	return n, err
}

// verifyIfPresent returns (true, nil) if path exists and matches the
// expected SHA256 (or verification is disabled with an empty expected
// hash). Returns (false, nil) if path doesn't exist or the hash
// differs — so the caller should proceed with a fresh download.
// Only (_, err) is returned for IO errors the caller can't recover
// from.
func verifyIfPresent(path, expectedSHA string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if expectedSHA == "" {
		// File exists and no hash to check against — treat as
		// already-downloaded; the caller is opting out of
		// verification via AllowUnpinned.
		return true, nil
	}
	if err := verifySHA(path, expectedSHA); err != nil {
		return false, nil
	}
	return true, nil
}

// verifySHA streams path through SHA-256 and compares the result
// case-insensitively against want. Returns a descriptive error on
// mismatch so the operator sees both hashes in the CLI output.
func verifySHA(path, want string) error {
	f, err := os.Open(path) // #nosec G304 -- path is thlibo-controlled
	if err != nil {
		return fmt.Errorf("install: open for verify: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("install: hash: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !equalFold(got, want) {
		return fmt.Errorf("install: sha256 mismatch for %s\n  got:  %s\n  want: %s", path, got, want)
	}
	return nil
}

// validateModelURL keeps us from accidentally fetching a non-
// HuggingFace source. A Model struct could have any URL; this
// narrow check catches configuration mistakes without blocking a
// future mirror on a different domain.
//
// HTTPS is required for any public host; HTTP is allowed only when
// the hostname resolves to a loopback address, so `httptest.Server`
// works in unit tests and operators can point at a trusted local
// mirror without flag-gymnastics.
func validateModelURL(raw string) error {
	if raw == "" {
		return errors.New("install: model URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("install: parse model URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("install: model URL must be https (got http for non-loopback host %q)", u.Hostname())
	default:
		return fmt.Errorf("install: model URL must be https (got %q)", u.Scheme)
	}
}

// isLoopbackHost matches `127.0.0.1`, `::1`, `localhost`, and the
// `127.x.x.x` range. We don't do DNS: the check is purely lexical,
// which is exactly what we want for a policy decision.
func isLoopbackHost(h string) bool {
	switch h {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return len(h) >= 4 && h[:4] == "127."
}

// equalFold is a case-insensitive string equality check without
// pulling in strings for one call.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
