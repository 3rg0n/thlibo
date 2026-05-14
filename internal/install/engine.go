package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Engine describes a pinned llamafile release binary.
type Engine struct {
	// Version is the upstream llamafile release tag, e.g. "0.10.1".
	Version string
	// URL is the direct-download link for this platform's binary.
	URL string
	// ExpectedSHA256 is the lowercase hex SHA-256 of the downloaded bytes.
	ExpectedSHA256 string
	// SizeBytes is advisory (progress + disk-space preflight).
	SizeBytes int64
}

// pinnedLlamafile0101 is the SHA-256 of llamafile-0.10.1 (the single
// universal binary, not the thin variant), downloaded 2026-05-14,
// 877,827,906 bytes. Overridable at build time via
//
//	-ldflags "-X github.com/3rg0n/thlibo/internal/install.pinnedLlamafile0101=<hash>"
//
// so a release can refresh the pin without a source change.
var pinnedLlamafile0101 = "3cc6ea1a5af07813d6c9f7459a64ec28345352c8322d2b355b6715847871bc13"

// DefaultEngine is the pinned llamafile release thlibod uses as its
// inference engine.  llamafile ships a single polyglot binary that
// runs on Linux, macOS, and Windows without separate builds.
var DefaultEngine = Engine{
	Version:        "0.10.1",
	URL:            "https://github.com/mozilla-ai/llamafile/releases/download/0.10.1/llamafile-0.10.1",
	ExpectedSHA256: pinnedLlamafile0101,
	SizeBytes:      877_827_906,
}

// EngineDir returns the directory where thlibo-engine is installed.
// Defaults to the same directory as the running thlibo binary so
// thlibod finds it without a -engine flag.
func EngineDir() string {
	if d := os.Getenv("THLIBO_ENGINE_DIR"); d != "" {
		return d
	}
	self, err := os.Executable()
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "bin")
	}
	return filepath.Dir(self)
}

// EngineName returns the platform-appropriate installed name.
func EngineName() string {
	if runtime.GOOS == "windows" {
		return "thlibo-engine.exe"
	}
	return "thlibo-engine"
}

// PullEngine downloads the engine binary to dir (defaults to EngineDir),
// verifies its SHA-256, makes it executable, and returns the installed
// path. Idempotent: if the file already exists with the correct hash it
// returns immediately.
func PullEngine(ctx context.Context, e Engine, opts PullOptions) (string, error) {
	if err := validateModelURL(e.URL); err != nil {
		return "", err
	}
	if e.ExpectedSHA256 == "" && !opts.AllowUnpinned {
		return "", fmt.Errorf("install: engine %q has no pinned SHA256; pass --allow-unpinned to download anyway", e.Version)
	}

	dir := opts.Dir
	if dir == "" {
		dir = EngineDir()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("install: create engine dir: %w", err)
	}

	final := filepath.Join(dir, EngineName())

	if ok, err := verifyIfPresent(final, e.ExpectedSHA256); err != nil {
		return "", err
	} else if ok {
		return final, nil
	}

	m := Model{
		URL:            e.URL,
		ExpectedSHA256: e.ExpectedSHA256,
		Filename:       EngineName(),
		SizeBytes:      e.SizeBytes,
	}
	path, err := Pull(ctx, m, PullOptions{
		Dir:           dir,
		Client:        opts.Client,
		AllowUnpinned: opts.AllowUnpinned,
		Progress:      opts.Progress,
		Resume:        opts.Resume,
		NoResume:      opts.NoResume,
	})
	if err != nil {
		return "", err
	}

	if err := os.Chmod(path, 0o755); err != nil { // #nosec G302 — engine binary must be world-executable
		return "", fmt.Errorf("install: chmod engine: %w", err)
	}
	return path, nil
}
