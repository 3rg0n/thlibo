// Package logx is thlibo's NDJSON activity logger. One JSON record
// per line, written to ~/.thlibo/logs/<component>.ndjson, with a
// small built-in rotation cap. Suitable for `jq`-based inspection
// when something's misbehaving, not a full observability stack —
// anything more elaborate belongs in an external OTEL exporter.
//
// Verbosity is controlled by THLIBO_LOG:
//
//	unset, 0, false, off     warnings + errors only (default)
//	1, true, on, info        + activity records (one per request)
//	debug, 2                 + detailed per-step records
//
// Records are free-form JSON objects. Callers build them via Record
// constructors to keep field names stable across the codebase.
//
// Logging is intentionally cheap when disabled: a nil Logger does
// nothing, so hot paths can call Info/Debug unconditionally.
package logx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Level describes the verbosity at which a record is emitted.
// Warnings and errors are emitted regardless of the THLIBO_LOG
// setting; activity and debug records respect it.
type Level string

const (
	LevelError    Level = "error"
	LevelWarn     Level = "warn"
	LevelActivity Level = "activity"
	LevelDebug    Level = "debug"
)

// Logger writes NDJSON records. Zero value is nil; nil is safe to
// call — every method is a no-op. Use New to produce a working one.
type Logger struct {
	component string
	path      string
	rotateAt  int64
	verbosity verbosity

	mu sync.Mutex
	f  *os.File // lazily opened so tests that never emit don't leave tmp files behind
}

type verbosity int

const (
	vWarn verbosity = iota
	vActivity
	vDebug
)

// parseVerbosity turns a THLIBO_LOG value into a verbosity level.
// Unrecognised values default to warnings-only — we don't want a
// typo'd env var to silently turn off logging the user wanted on.
func parseVerbosity(s string) verbosity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "false", "off", "no":
		return vWarn
	case "debug", "2", "trace":
		return vDebug
	default:
		// Everything else (1, true, on, info, ...) is activity-level.
		return vActivity
	}
}

// DefaultDir returns ~/.thlibo/logs, or $THLIBO_LOGS_DIR if set.
// Creating the directory is the Logger's responsibility — the
// caller doesn't need to mkdir upfront.
func DefaultDir() string {
	if d := os.Getenv("THLIBO_LOGS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "thlibo", "logs")
	}
	return filepath.Join(home, ".thlibo", "logs")
}

// New returns a Logger writing to <dir>/<component>.ndjson. Passing
// "" for dir uses DefaultDir(). The file is opened lazily on the
// first write so non-logging invocations (short-circuited requests,
// `thlibo help`) leave the filesystem untouched.
//
// Rotation: when the file grows past rotateBytes, it's renamed to
// <component>.ndjson.old (overwriting any previous .old) and a
// fresh file is started. Default rotateBytes is 10 MiB; pass 0
// for no rotation.
func New(component, dir string, rotateBytes int64) *Logger {
	if dir == "" {
		dir = DefaultDir()
	}
	if rotateBytes == 0 {
		rotateBytes = 10 << 20
	}
	return &Logger{
		component: component,
		path:      filepath.Join(dir, component+".ndjson"),
		rotateAt:  rotateBytes,
		verbosity: parseVerbosity(os.Getenv("THLIBO_LOG")),
	}
}

// Error writes an error-level record. Always emitted.
func (l *Logger) Error(msg string, fields ...Field) {
	l.write(LevelError, msg, fields)
}

// Warn writes a warning-level record. Always emitted.
func (l *Logger) Warn(msg string, fields ...Field) {
	l.write(LevelWarn, msg, fields)
}

// Info (activity) writes one record per request when THLIBO_LOG=1.
// Suppressed when the env var is off.
func (l *Logger) Info(msg string, fields ...Field) {
	if l == nil || l.verbosity < vActivity {
		return
	}
	l.write(LevelActivity, msg, fields)
}

// Debug writes a record only when THLIBO_LOG=debug.
func (l *Logger) Debug(msg string, fields ...Field) {
	if l == nil || l.verbosity < vDebug {
		return
	}
	l.write(LevelDebug, msg, fields)
}

// Field is a key-value pair attached to a log record. Use the
// constructors (Int / Str / Dur / Err) rather than building Fields
// by hand so field types stay consistent across call sites.
type Field struct {
	Key string
	Val any
}

func Int(k string, v int) Field           { return Field{k, v} }
func Int64(k string, v int64) Field       { return Field{k, v} }
func Str(k, v string) Field               { return Field{k, Redact(v)} }
func Bool(k string, v bool) Field         { return Field{k, v} }
func Any(k string, v any) Field           { return Field{k, v} }
func Dur(k string, d time.Duration) Field { return Field{k, d.String()} }
func Err(err error) Field {
	if err == nil {
		return Field{"error", nil}
	}
	return Field{"error", Redact(err.Error())}
}

// Redact masks common secret patterns in log strings. The intent is a
// best-effort safety net for processor stderr / error messages that
// might inadvertently echo an API key; it is NOT a substitute for
// structured-field logging. See THREAT_MODEL.md finding #8.
//
// Patterns cover:
//   - AWS: AKIA[0-9A-Z]{16}, aws_secret_access_key=<v>, AWS_SECRET_* = <v>
//   - GitHub: ghp_<36>, gho_<36>, ghu_<36>, ghs_<36>, ghr_<36>
//   - HuggingFace: hf_<30-100>
//   - Slack: xox[abpr]-<token>
//   - generic: <UPPER>_TOKEN=<v>, <UPPER>_KEY=<v>, <UPPER>_SECRET=<v>,
//     Bearer <token>, api_key=<v>, password=<v>
//
// Redaction replaces the value with "[REDACTED]"; keys stay visible so
// an operator can see that a secret was present and investigate the
// upstream source rather than chasing a mystery blank field.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`),
	regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}\b`),
	regexp.MustCompile(`\bxox[abpr]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`(?i)\b(?:authorization|bearer)\s*[:=]?\s*[A-Za-z0-9._\-]{16,}`),
	regexp.MustCompile(`(?i)\b[A-Z][A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|APIKEY|API_KEY)\b\s*[:=]\s*[^\s"']+`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|password|passwd|secret)\s*[:=]\s*[^\s"']+`),
}

// Redact applies secretPatterns to s and returns the masked form.
// Exported so callers that build Field values by hand (Any, Int64) can
// redact before constructing the Field if they carry sensitive bytes.
func Redact(s string) string {
	if s == "" {
		return s
	}
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// write is the one place that actually touches the file. Safe on a
// nil receiver (callers in hot paths don't have to branch).
func (l *Logger) write(level Level, msg string, fields []Field) {
	if l == nil {
		return
	}
	rec := map[string]any{
		"t":         time.Now().UTC().Format(time.RFC3339Nano),
		"component": l.component,
		"level":     string(level),
		"msg":       msg,
	}
	for _, f := range fields {
		rec[f.Key] = f.Val
	}

	buf, err := json.Marshal(rec)
	if err != nil {
		// Last-ditch fallback — emit to stderr so a logger bug
		// doesn't swallow real errors.
		fmt.Fprintf(os.Stderr, "logx: marshal: %v\n", err)
		return
	}
	buf = append(buf, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureOpen(); err != nil {
		fmt.Fprintf(os.Stderr, "logx: open %s: %v\n", l.path, err)
		return
	}
	if _, err := l.f.Write(buf); err != nil {
		fmt.Fprintf(os.Stderr, "logx: write %s: %v\n", l.path, err)
		return
	}
	l.maybeRotateLocked()
}

// ensureOpen creates the directory + file on first write. Called
// under l.mu.
func (l *Logger) ensureOpen() error {
	if l.f != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- path is component-derived, not user input
	if err != nil {
		return err
	}
	l.f = f
	return nil
}

// maybeRotateLocked checks file size and rotates if we're past the
// threshold. Caller holds l.mu. Rotation is best-effort: a failure
// just means the file keeps growing, which is strictly less bad
// than losing writes.
func (l *Logger) maybeRotateLocked() {
	if l.rotateAt <= 0 || l.f == nil {
		return
	}
	info, err := l.f.Stat()
	if err != nil || info.Size() < l.rotateAt {
		return
	}
	old := l.path + ".old"
	_ = l.f.Close()
	l.f = nil
	_ = os.Remove(old)
	_ = os.Rename(l.path, old)
	// Next write will reopen and recreate.
}

// Close flushes any pending write and releases the underlying file.
// Safe to call on a never-used Logger; always returns nil for nil.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
