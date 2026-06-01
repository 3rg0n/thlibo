package logx

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNilLoggerIsNoOp: a nil *Logger on every public method must
// not panic. Hot paths rely on this.
func TestNilLoggerIsNoOp(t *testing.T) {
	var l *Logger
	l.Debug("x")
	l.Info("x")
	l.Warn("x")
	l.Error("x")
	if err := l.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}

// TestDefaultVerbosityIsWarn: without THLIBO_LOG, only warn+error
// emit; activity and debug are dropped.
func TestDefaultVerbosityIsWarn(t *testing.T) {
	t.Setenv("THLIBO_LOG", "")
	dir := t.TempDir()
	l := New("test", dir, 0)
	defer l.Close()

	l.Debug("d")
	l.Info("i")
	l.Warn("w", Str("k", "v"))
	l.Error("e", Err(errors.New("boom")))

	recs := readRecords(t, filepath.Join(dir, "test.ndjson"))
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2 (warn+error): %+v", len(recs), recs)
	}
	if recs[0]["level"] != "warn" || recs[1]["level"] != "error" {
		t.Errorf("unexpected levels: %+v", recs)
	}
	if recs[0]["k"] != "v" {
		t.Errorf("field k lost: %+v", recs[0])
	}
	if recs[1]["error"] != "boom" {
		t.Errorf("error field: %+v", recs[1])
	}
}

// TestActivityVerbosity: THLIBO_LOG=1 promotes activity records to
// the file; debug is still dropped.
func TestActivityVerbosity(t *testing.T) {
	t.Setenv("THLIBO_LOG", "1")
	dir := t.TempDir()
	l := New("test", dir, 0)
	defer l.Close()

	l.Debug("d")
	l.Info("i", Int("n", 42))
	l.Warn("w")

	recs := readRecords(t, filepath.Join(dir, "test.ndjson"))
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2 (activity+warn): %+v", len(recs), recs)
	}
	if recs[0]["level"] != "activity" {
		t.Errorf("first level = %v", recs[0]["level"])
	}
	// JSON round-trip: integer becomes float64 in the generic map.
	if n, _ := recs[0]["n"].(float64); n != 42 {
		t.Errorf("n = %v, want 42", recs[0]["n"])
	}
}

// TestDebugVerbosity: THLIBO_LOG=debug writes every level.
func TestDebugVerbosity(t *testing.T) {
	t.Setenv("THLIBO_LOG", "debug")
	dir := t.TempDir()
	l := New("test", dir, 0)
	defer l.Close()

	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")

	recs := readRecords(t, filepath.Join(dir, "test.ndjson"))
	if len(recs) != 4 {
		t.Errorf("records = %d, want 4: %+v", len(recs), recs)
	}
}

// TestLazyOpen: a Logger that never emits leaves no file behind.
func TestLazyOpen(t *testing.T) {
	t.Setenv("THLIBO_LOG", "")
	dir := t.TempDir()
	l := New("test", dir, 0)
	l.Debug("never emitted")
	l.Info("never emitted")
	if _, err := os.Stat(filepath.Join(dir, "test.ndjson")); !os.IsNotExist(err) {
		t.Errorf("log file exists after no-op logging: %v", err)
	}
	_ = l.Close()
}

// TestDailyRotation: when the calendar day rolls over, the live
// file is renamed to `<component>-<previous-day>.ndjson` and a
// fresh live file is opened. Records emitted on the new day land
// in the new live file; records emitted on the previous day live
// in the archive.
func TestDailyRotation(t *testing.T) {
	t.Setenv("THLIBO_LOG", "1")
	dir := t.TempDir()
	l := New("test", dir, 0)
	defer l.Close()

	day1 := time.Date(2026, 5, 26, 12, 0, 0, 0, time.Local)
	day2 := day1.Add(24 * time.Hour)
	l.now = func() time.Time { return day1 }
	l.Info("yesterday")

	l.now = func() time.Time { return day2 }
	l.Info("today")

	archive := filepath.Join(dir, "test-2026-05-26.ndjson")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("archive %s not created: %v", archive, err)
	}
	old := readRecords(t, archive)
	if len(old) != 1 || old[0]["msg"] != "yesterday" {
		t.Errorf("archive contents wrong: %+v", old)
	}
	live := readRecords(t, filepath.Join(dir, "test.ndjson"))
	if len(live) != 1 || live[0]["msg"] != "today" {
		t.Errorf("live contents wrong: %+v", live)
	}
}

// TestRetentionSweep: archives older than retainDays are deleted
// on the first write of a day that triggers rotation. Younger
// archives, the live file, and unrelated files in the directory
// must all survive the sweep.
func TestRetentionSweep(t *testing.T) {
	t.Setenv("THLIBO_LOG", "1")
	dir := t.TempDir()

	old8 := filepath.Join(dir, "test-2026-05-18.ndjson") // 8 days ago
	old3 := filepath.Join(dir, "test-2026-05-23.ndjson") // 3 days ago
	other := filepath.Join(dir, "other-component-2020-01-01.ndjson")
	for _, p := range []string{old8, old3, other} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	l := New("test", dir, 7)
	defer l.Close()
	day1 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.Local)
	day2 := day1.Add(24 * time.Hour)
	l.now = func() time.Time { return day1 }
	l.Info("seed live file")
	l.now = func() time.Time { return day2 }
	l.Info("trigger sweep")

	if _, err := os.Stat(old8); !os.IsNotExist(err) {
		t.Errorf("8-day-old archive not swept: err=%v", err)
	}
	if _, err := os.Stat(old3); err != nil {
		t.Errorf("3-day-old archive should have survived: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("foreign component file must not be touched: %v", err)
	}
}

// TestRetainDaysFromEnv: THLIBO_LOG_RETAIN_DAYS overrides the
// 7-day default, including malformed values that should fall back
// to the default rather than disable retention silently.
func TestRetainDaysFromEnv(t *testing.T) {
	cases := []struct {
		env  string
		want int
	}{
		{"", defaultRetainDays},
		{"14", 14},
		{"0", defaultRetainDays},   // non-positive falls back, never disables
		{"-3", defaultRetainDays},  // negative falls back
		{"abc", defaultRetainDays}, // malformed falls back
	}
	for _, tc := range cases {
		t.Setenv("THLIBO_LOG_RETAIN_DAYS", tc.env)
		if got := retainDaysFromEnv(); got != tc.want {
			t.Errorf("env=%q: got %d, want %d", tc.env, got, tc.want)
		}
	}
}

// TestRotationFromMtime: when the live file already exists from a
// prior process and we open it on a later day, the prior content
// is archived under the file's mtime date — not lost into today's
// log.
func TestRotationFromMtime(t *testing.T) {
	t.Setenv("THLIBO_LOG", "1")
	dir := t.TempDir()
	livePath := filepath.Join(dir, "test.ndjson")
	if err := os.WriteFile(livePath, []byte(`{"msg":"prior-day"}`+"\n"), 0o600); err != nil {
		t.Fatalf("seed live: %v", err)
	}
	priorDay := time.Date(2026, 5, 20, 9, 0, 0, 0, time.Local)
	if err := os.Chtimes(livePath, priorDay, priorDay); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	l := New("test", dir, 7)
	defer l.Close()
	l.now = func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.Local) }
	l.Info("today")

	archive := filepath.Join(dir, "test-2026-05-20.ndjson")
	if _, err := os.Stat(archive); err != nil {
		t.Errorf("archive for prior day not created: %v", err)
	}
	live := readRecords(t, livePath)
	if len(live) != 1 || live[0]["msg"] != "today" {
		t.Errorf("live should contain only today's record: %+v", live)
	}
}

// TestRecordShape: every record has t/component/level/msg.
func TestRecordShape(t *testing.T) {
	t.Setenv("THLIBO_LOG", "1")
	dir := t.TempDir()
	l := New("my-component", dir, 0)
	defer l.Close()

	l.Info("a message", Str("extra", "yes"))

	recs := readRecords(t, filepath.Join(dir, "my-component.ndjson"))
	if len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}
	for _, k := range []string{"t", "component", "level", "msg"} {
		if _, ok := recs[0][k]; !ok {
			t.Errorf("missing %q: %+v", k, recs[0])
		}
	}
	if recs[0]["component"] != "my-component" {
		t.Errorf("component = %v", recs[0]["component"])
	}
}

// TestParseVerbosity covers the env-parser table. Policy:
// unrecognised values intentionally fall through to activity level
// so a typo'd env var never silently disables logging the user
// meant to turn on.
func TestParseVerbosity(t *testing.T) {
	warn := []string{"", "0", "false", "off", "no"}
	active := []string{"1", "true", "on", "info", "yes", "invalid-token"}
	debug := []string{"debug", "2", "trace", "DEBUG"}

	for _, s := range warn {
		if got := parseVerbosity(s); got != vWarn {
			t.Errorf("parseVerbosity(%q) = %d, want warn(%d)", s, got, vWarn)
		}
	}
	for _, s := range active {
		if got := parseVerbosity(s); got != vActivity {
			t.Errorf("parseVerbosity(%q) = %d, want activity(%d)", s, got, vActivity)
		}
	}
	for _, s := range debug {
		if got := parseVerbosity(s); got != vDebug {
			t.Errorf("parseVerbosity(%q) = %d, want debug(%d)", s, got, vDebug)
		}
	}
}

// TestDefaultDirHonorsEnv: THLIBO_LOGS_DIR beats the default.
func TestDefaultDirHonorsEnv(t *testing.T) {
	t.Setenv("THLIBO_LOGS_DIR", "/custom/logs")
	if got := DefaultDir(); got != "/custom/logs" {
		t.Errorf("DefaultDir = %q", got)
	}
}

// readRecords parses an NDJSON file into a slice of generic maps.
// Used by every test assertion above; lives here (not in its own
// helper file) to keep the test package self-contained.
func readRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- path is t.TempDir-derived
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<16)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("parse line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}
