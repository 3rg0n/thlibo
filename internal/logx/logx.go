// Package logx is thlibo's NDJSON activity logger. One JSON record
// per line, written to ~/.thlibo/logs/<component>.ndjson, with a
// daily-rotation + age-based retention sweep. Suitable for `jq`-based
// inspection when something's misbehaving, not a full observability
// stack — anything more elaborate belongs in an external OTEL exporter.
//
// Verbosity is controlled by THLIBO_LOG:
//
//	unset, 0, false, off     warnings + errors only (default)
//	1, true, on, info        + activity records (one per request)
//	debug, 2                 + detailed per-step records
//
// Retention is controlled by THLIBO_LOG_RETAIN_DAYS (default 7):
// historic files (`<component>-YYYY-MM-DD.ndjson`) older than the
// retention window are deleted on the first write of each day. The
// live `<component>.ndjson` is never deleted by the sweep.
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
	"strconv"
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
	component   string
	dir         string
	path        string
	retainDays  int
	verbosity   verbosity
	now         func() time.Time // injectable for tests; defaults to time.Now

	mu     sync.Mutex
	f      *os.File // lazily opened so tests that never emit don't leave tmp files behind
	openOn string   // YYYY-MM-DD the live file was opened against; "" = not yet open
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
// Rotation: when the calendar day rolls over, the live file is
// renamed to `<component>-YYYY-MM-DD.ndjson` (the closing day's
// date) and a fresh live file is opened. On each rotation the
// directory is swept for archives older than retainDays and any
// stale ones are deleted. retainDays <= 0 means use the
// THLIBO_LOG_RETAIN_DAYS env var or fall back to defaultRetainDays
// (7). Pass a positive int to override per-call.
//
// Why daily, not size-based: operators expect "the last week of
// activity, broken up by day" for jq-friendly debugging. A size cap
// would either truncate a busy day mid-stream or accumulate noise
// indefinitely on a quiet host.
func New(component, dir string, retainDays int) *Logger {
	if dir == "" {
		dir = DefaultDir()
	}
	if retainDays <= 0 {
		retainDays = retainDaysFromEnv()
	}
	return &Logger{
		component:  component,
		dir:        dir,
		path:       filepath.Join(dir, component+".ndjson"),
		retainDays: retainDays,
		verbosity:  parseVerbosity(os.Getenv("THLIBO_LOG")),
		now:        time.Now,
	}
}

// defaultRetainDays is the FIFO window: archives older than this
// many days from the current local-day boundary are deleted. Seven
// matches "show me last week" and bounds disk use even on chatty
// hosts (typical activity record is ~200 bytes; 7 days * 1 file/day
// keeps the directory tidy).
const defaultRetainDays = 7

// retainDaysFromEnv reads THLIBO_LOG_RETAIN_DAYS, falling back to
// defaultRetainDays when the env var is unset, malformed, or non-
// positive. We deliberately ignore parse errors silently — a typo'd
// retention value should not break logging.
func retainDaysFromEnv() int {
	v := strings.TrimSpace(os.Getenv("THLIBO_LOG_RETAIN_DAYS"))
	if v == "" {
		return defaultRetainDays
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultRetainDays
	}
	return n
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
	today := l.now().Format("2006-01-02")
	if err := l.ensureOpenLocked(today); err != nil {
		fmt.Fprintf(os.Stderr, "logx: open %s: %v\n", l.path, err)
		return
	}
	if _, err := l.f.Write(buf); err != nil {
		fmt.Fprintf(os.Stderr, "logx: write %s: %v\n", l.path, err)
		return
	}
}

// ensureOpenLocked creates the directory + opens the live file on
// first write, and rotates+sweeps when the calendar day rolls over.
// Caller holds l.mu. The today argument is the current local day in
// "2006-01-02" form; passing it in (rather than calling l.now()
// here) keeps the test seam at one place.
//
// Day-rollover behaviour:
//  1. If the file is already open and openOn matches today, no-op.
//  2. If the file is already open and openOn != today, close it,
//     rename `<component>.ndjson` → `<component>-<openOn>.ndjson`
//     (the closing day's date), and open a fresh live file.
//  3. If the file is not open yet, infer the prior day from the
//     existing file's mtime: if it's not today, do the same rename
//     before opening. This makes rotation correct even when thlibo
//     wasn't running at midnight.
//
// The retention sweep runs once per Logger lifetime, on the first
// rotation we trigger, so a hot path that writes 1k records in a
// burst doesn't rescan the directory 1k times.
func (l *Logger) ensureOpenLocked(today string) error {
	if l.f != nil && l.openOn == today {
		return nil
	}
	if err := os.MkdirAll(l.dir, 0o750); err != nil {
		return err
	}
	if l.f != nil && l.openOn != "" && l.openOn != today {
		_ = l.f.Close()
		l.f = nil
		l.archiveLocked(l.openOn)
		l.sweepLocked(today)
	} else if l.f == nil {
		if info, err := os.Stat(l.path); err == nil {
			fileDay := info.ModTime().Local().Format("2006-01-02")
			if fileDay != today {
				l.archiveLocked(fileDay)
				l.sweepLocked(today)
			}
		}
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- path is component-derived, not user input
	if err != nil {
		return err
	}
	l.f = f
	l.openOn = today
	return nil
}

// archiveLocked renames the live `<component>.ndjson` to a
// date-suffixed archive. Missing-source is fine (a brand-new
// component never had a live file). If the target name already
// exists (someone manually rotated, or two thlibo processes
// raced through the same midnight), we leave the archive as-is
// and let the live file truncate — preserving the older record
// is more useful than overwriting it.
func (l *Logger) archiveLocked(day string) {
	src := l.path
	dst := l.archivePath(day)
	if _, err := os.Stat(src); err != nil {
		return
	}
	if _, err := os.Stat(dst); err == nil {
		// Append today's content to the existing archive instead of
		// clobbering. Best-effort; on failure we leave src in place
		// (the next call will retry).
		if data, rerr := os.ReadFile(src); rerr == nil { // #nosec G304 -- path is component-derived
			if f, oerr := os.OpenFile(dst, os.O_APPEND|os.O_WRONLY, 0o600); oerr == nil { // #nosec G304 -- path is component-derived
				_, _ = f.Write(data)
				_ = f.Close()
				_ = os.Remove(src)
			}
		}
		return
	}
	_ = os.Rename(src, dst)
}

// sweepLocked deletes archives older than retainDays from today.
// Best-effort: a failed Remove just leaves the file in place, which
// is strictly less bad than dropping live writes. We only touch
// files matching the `<component>-YYYY-MM-DD.ndjson` pattern so we
// can't delete unrelated user files even if THLIBO_LOGS_DIR is
// pointed at a shared directory.
func (l *Logger) sweepLocked(today string) {
	cutoff, err := time.ParseInLocation("2006-01-02", today, time.Local)
	if err != nil {
		return
	}
	cutoff = cutoff.AddDate(0, 0, -l.retainDays)
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	prefix := l.component + "-"
	const suffix = ".ndjson"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		t, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(l.dir, name))
		}
	}
}

// archivePath returns the on-disk name for an archived day. Format
// is `<component>-YYYY-MM-DD.ndjson` so jq-friendly globbing works
// (`cat ~/.thlibo/logs/thlibo-exec-*.ndjson | jq -c .`).
func (l *Logger) archivePath(day string) string {
	return filepath.Join(l.dir, l.component+"-"+day+".ndjson")
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
