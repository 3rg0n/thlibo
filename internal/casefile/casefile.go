// Package casefile writes "case" directories for large log files
// that Claude Code is about to read. Each case captures:
//
//   ~/.thlibo/cases/<timestamp>-<hash>/
//     compressed.log    — output of middleware.Pipeline over the source
//     summary.md        — human-readable header (origin, sizes, time)
//     meta.json         — structured record for tooling
//
// The primary consumer is the Read-tool PreToolUse hook, which builds
// a case and rewrites tool_input.file_path to compressed.log so Claude
// reads the compressed form instead of the original 200 MB blob. The
// `thlibo case` CLI subcommand exposes the same primitive for
// scripted use and for the /caselog skill.
//
// Security model:
//
//   - Cases live under $HOME (per-user; no shared directory).
//   - Directory mode 0o700, file mode 0o600 — matches logx and the
//     rest of ~/.thlibo.
//   - Retention is not the library's job: `thlibo case --prune` is a
//     separate CLI concern.
//   - Source file is opened with the caller's credentials; if they
//     can't read it, we surface the error to the caller.
//
// This package does NOT start the daemon, reach the network, or
// modify any file outside the cases directory.
package casefile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/3rg0n/thlibo/internal/middleware"
)

// Meta is the structured record persisted as meta.json.
type Meta struct {
	// ID is the case directory name (timestamp-hash slug).
	ID string `json:"id"`
	// SourcePath is the absolute path to the original file.
	SourcePath string `json:"source_path"`
	// SourceSize is the original file size in bytes.
	SourceSize int64 `json:"source_size"`
	// CompressedSize is the size of compressed.log in bytes.
	CompressedSize int64 `json:"compressed_size"`
	// ReductionPercent is 100 * (1 - compressed/source). Rounded
	// to two decimals.
	ReductionPercent float64 `json:"reduction_percent"`
	// CreatedAt is when the case was written.
	CreatedAt time.Time `json:"created_at"`
	// Fallback is true iff the compression pipeline errored out
	// and compressed.log contains a verbatim copy of the source.
	// Set by callers that can distinguish fallback from success;
	// Create leaves it at the zero value.
	Fallback bool `json:"fallback,omitempty"`
	// LowValue is true iff the pipeline produced output that's
	// structurally a success but carries no usable signal — e.g. a
	// scanned-image PDF where every page returns an "OCR not yet
	// supported" placeholder. Callers (the Read hook) should treat
	// LowValue cases as "don't divert the read; let the upstream
	// reader handle it natively." See issue #31.
	LowValue bool `json:"low_value,omitempty"`
	// ThliboVersion is the build tag of the tool that wrote the
	// case. Filled in from internal/version.Tag by callers.
	ThliboVersion string `json:"thlibo_version,omitempty"`
}

// lowValueSentinel is the line pdf-to-md emits when every page is
// scanned/blank/chart with no extractable text. Format is loud
// enough that nothing else produces it accidentally; matched
// substring-wise so we don't have to care about trailing whitespace.
const lowValueSentinel = "<!-- thlibo-pdf-low-value:"

// Result bundles the directory and meta back to the caller.
type Result struct {
	Dir           string // absolute path to the case directory
	CompressedLog string // absolute path to compressed.log
	Meta          Meta
}

// Options controls Create.
type Options struct {
	// CasesRoot overrides the default ~/.thlibo/cases root. Empty
	// uses the default; DefaultCasesRoot resolves $HOME.
	CasesRoot string
	// Now overrides time.Now for deterministic tests. Zero means
	// use the real clock.
	Now time.Time
	// ThliboVersion is stamped into meta.json. Callers pass
	// version.Tag; empty is acceptable (just omitted from meta).
	ThliboVersion string
	// Pipeline is the middleware pipeline used for compression.
	// Nil means "skip compression, copy source verbatim and set
	// Meta.Fallback" — used when the daemon is unreachable so
	// we can still produce a case directory.
	Pipeline *middleware.Pipeline
}

// DefaultCasesRoot returns ~/.thlibo/cases, or a tmp fallback if the
// home dir can't be resolved.
func DefaultCasesRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "thlibo", "cases")
	}
	return filepath.Join(home, ".thlibo", "cases")
}

// ErrSourceNotRegular is returned when the source path is a device,
// socket, FIFO, or directory.
var ErrSourceNotRegular = errors.New("casefile: source must be a regular file")

// Create builds a case directory for sourcePath. It:
//
//  1. Resolves the absolute source path.
//  2. Stats the source (regular-file check).
//  3. Allocates a per-case directory under opts.CasesRoot.
//  4. Streams source → middleware.Process → compressed.log.
//  5. Writes summary.md and meta.json.
//
// On any IO error the partial directory is removed; callers see an
// error and never a half-written case.
func Create(ctx context.Context, sourcePath string, opts Options) (*Result, error) {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("casefile: resolve source: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("casefile: stat source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s (mode %s)", ErrSourceNotRegular, abs, info.Mode())
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	root := opts.CasesRoot
	if root == "" {
		root = DefaultCasesRoot()
	}
	id := caseID(abs, now)
	dir := filepath.Join(root, id)

	// Directory 0o700 — match logx / ~/.thlibo.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("casefile: create dir: %w", err)
	}
	// If anything below fails, scrub the directory so callers don't
	// find a half-built case on disk.
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()

	raw, err := os.ReadFile(abs) // #nosec G304 -- abs comes from the caller, who owns auth (hook/CLI user)
	if err != nil {
		return nil, fmt.Errorf("casefile: read source: %w", err)
	}

	compressedPath := filepath.Join(dir, "compressed.log")
	var compressed []byte
	fallback := opts.Pipeline == nil
	if !fallback {
		var buf bytes.Buffer
		if perr := opts.Pipeline.Process(ctx, bytes.NewReader(raw), &buf); perr != nil {
			// middleware.Pipeline.Process never returns non-nil
			// in current code, but treat any future error as a
			// fallback rather than propagating — same contract as
			// every other hook path in thlibo.
			fallback = true
		}
		compressed = buf.Bytes()
		if len(compressed) == 0 {
			// Pipeline wrote nothing (e.g. empty input short-circuit
			// with no bytes); copy raw verbatim.
			compressed = raw
			fallback = true
		}
	} else {
		compressed = raw
	}

	// Detect and strip the low-value sentinel emitted by pdf-to-md
	// when every page is a placeholder. We strip it so a downstream
	// reader (humans opening the case dir; the /caselog skill) sees
	// only the real content; the LowValue flag in meta.json carries
	// the signal forward for tooling that needs it.
	lowValue := false
	if bytes.Contains(compressed, []byte(lowValueSentinel)) {
		lowValue = true
		compressed = stripSentinelLine(compressed)
	}

	// #nosec G703 -- gosec's taint analysis follows compressedPath
	// back to sourcePath (user-supplied). The actual string at this
	// line is filepath.Join(<casesRoot>, caseID(...), "compressed.log")
	// where caseID is "<UTC timestamp>-<sha256 hex prefix>"; no
	// component of compressedPath is caller-controlled in a way that
	// could traverse outside casesRoot.
	if err := os.WriteFile(compressedPath, compressed, 0o600); err != nil {
		return nil, fmt.Errorf("casefile: write compressed.log: %w", err)
	}

	meta := Meta{
		ID:               id,
		SourcePath:       abs,
		SourceSize:       info.Size(),
		CompressedSize:   int64(len(compressed)),
		ReductionPercent: reductionPct(info.Size(), int64(len(compressed))),
		CreatedAt:        now,
		Fallback:         fallback,
		LowValue:         lowValue,
		ThliboVersion:    opts.ThliboVersion,
	}

	if err := writeMeta(dir, meta); err != nil {
		return nil, err
	}
	if err := writeSummary(dir, meta); err != nil {
		return nil, err
	}

	success = true
	return &Result{Dir: dir, CompressedLog: compressedPath, Meta: meta}, nil
}

// caseID builds a sortable, unique case directory name. Format:
//
//	YYYYMMDD-HHMMSS-<8-hex>
//
// The hex is a SHA-256 prefix of the source path so two cases
// against the same log on the same second don't collide, but
// repeated runs against different files at the same second still
// get different IDs.
func caseID(sourcePath string, now time.Time) string {
	ts := now.UTC().Format("20060102-150405")
	sum := sha256.Sum256([]byte(sourcePath))
	return fmt.Sprintf("%s-%s", ts, hex.EncodeToString(sum[:4]))
}

func writeMeta(dir string, meta Meta) error {
	buf, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("casefile: marshal meta: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), buf, 0o600)
}

// writeSummary emits a short markdown header pointing back at the
// source. Intended for humans opening the case dir later, and for
// Claude to have a quick-read version of "what is this file".
func writeSummary(dir string, meta Meta) error {
	verLine := ""
	if meta.ThliboVersion != "" {
		verLine = "- thlibo: " + meta.ThliboVersion + "\n"
	}
	fbNote := ""
	if meta.Fallback {
		fbNote = "- **Note:** compression pipeline unavailable; compressed.log is a verbatim copy of the source.\n"
	}
	lvNote := ""
	if meta.LowValue {
		lvNote = "- **Note:** compressed.log contains placeholder content only (e.g. scanned PDF, OCR not yet supported). The Read hook should have let the original read pass through.\n"
	}
	body := fmt.Sprintf(`# thlibo case %s

- source: %s
- captured: %s
- source size: %d bytes
- compressed size: %d bytes
- reduction: %.2f%%
%s%s%s`,
		meta.ID, meta.SourcePath, meta.CreatedAt.Format(time.RFC3339),
		meta.SourceSize, meta.CompressedSize, meta.ReductionPercent,
		verLine, fbNote, lvNote)
	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(body), 0o600)
}

// stripSentinelLine removes any line containing lowValueSentinel
// from buf and returns the remainder. We strip whole lines (not
// just the marker) so the surrounding text doesn't read as if a
// trailing comment got cut off mid-stream.
func stripSentinelLine(buf []byte) []byte {
	marker := []byte(lowValueSentinel)
	if !bytes.Contains(buf, marker) {
		return buf
	}
	lines := bytes.Split(buf, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if bytes.Contains(line, marker) {
			continue
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n"))
}

func reductionPct(source, compressed int64) float64 {
	if source <= 0 {
		return 0
	}
	raw := (1.0 - float64(compressed)/float64(source)) * 100
	// math.Round does half-away-from-zero; important for the
	// negative-reduction case (compressed > source) where plain
	// int64(x+0.5) would round toward zero instead.
	return math.Round(raw*100) / 100
}

// Prune deletes case directories older than maxAge. Errors on
// individual case directories are logged to stderr-like logger (nil
// = silent) and don't abort the walk. Returns count of pruned
// directories.
func Prune(root string, maxAge time.Duration, now time.Time, logf func(string, ...any)) (int, error) {
	if root == "" {
		root = DefaultCasesRoot()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cutoff := now.Add(-maxAge)

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("casefile: read cases dir: %w", err)
	}

	pruned := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		target := filepath.Join(root, e.Name())
		if err := os.RemoveAll(target); err != nil {
			if logf != nil {
				logf("prune %s: %v", target, err)
			}
			continue
		}
		pruned++
	}
	return pruned, nil
}

// ensure io is used (keeps the import list stable across refactors).
var _ = io.Discard
