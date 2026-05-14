package casefile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// When no pipeline is supplied, Create must produce a case directory
// with compressed.log == source bytes verbatim and meta.Fallback=true.
func TestCreateWithoutPipelineIsFallback(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "noisy.log")
	body := []byte("line1\nline2\nline3\n")
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Create(context.Background(), src, Options{
		CasesRoot: root,
		Now:       time.Date(2026, 5, 14, 15, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Meta.Fallback != true {
		t.Errorf("expected Fallback=true without pipeline")
	}
	got, err := os.ReadFile(res.CompressedLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("fallback must copy bytes verbatim; got %q", got)
	}
	// meta.json structure.
	raw, _ := os.ReadFile(filepath.Join(res.Dir, "meta.json"))
	var m Meta
	_ = json.Unmarshal(raw, &m)
	if m.SourceSize != int64(len(body)) {
		t.Errorf("SourceSize=%d, want %d", m.SourceSize, len(body))
	}
	if m.ID == "" || !strings.HasPrefix(m.ID, "20260514-153000-") {
		t.Errorf("unexpected ID %q", m.ID)
	}
	// summary.md exists and contains the source path.
	sum, _ := os.ReadFile(filepath.Join(res.Dir, "summary.md"))
	if !strings.Contains(string(sum), src) {
		t.Errorf("summary.md missing source path: %s", sum)
	}
}

// A directory source must be rejected: no silent "compressed some
// directory listing" behaviour.
func TestCreateRejectsNonRegularSource(t *testing.T) {
	root := t.TempDir()
	_, err := Create(context.Background(), t.TempDir(), Options{CasesRoot: root})
	if err == nil {
		t.Fatal("expected error for directory source")
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Errorf("error should mention regular file; got %v", err)
	}
}

// Prune removes directories older than maxAge and leaves fresh ones.
func TestPruneAge(t *testing.T) {
	root := t.TempDir()

	stale := filepath.Join(root, "stale")
	fresh := filepath.Join(root, "fresh")
	for _, d := range []string{stale, fresh} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(stale, old, old)

	n, err := Prune(root, 24*time.Hour, time.Now(), nil)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale dir should be gone")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh dir should remain: %v", err)
	}
}

// Missing cases root is not an error — Prune returns 0, nil so
// callers can call it unconditionally.
func TestPruneMissingRootIsNoop(t *testing.T) {
	n, err := Prune(filepath.Join(t.TempDir(), "never-created"), 24*time.Hour, time.Time{}, nil)
	if err != nil {
		t.Errorf("missing root should not error: %v", err)
	}
	if n != 0 {
		t.Errorf("pruned %d, want 0", n)
	}
}

// ReductionPercent math and rounding.
func TestReductionPct(t *testing.T) {
	cases := []struct {
		src, dst int64
		want     float64
	}{
		{1000, 100, 90.00},
		{1000, 1000, 0},
		{1000, 0, 100},
		{0, 0, 0}, // guard against divide-by-zero
		{1000, 1500, -50.00},
	}
	for _, tc := range cases {
		if got := reductionPct(tc.src, tc.dst); got != tc.want {
			t.Errorf("reductionPct(%d, %d) = %v, want %v", tc.src, tc.dst, got, tc.want)
		}
	}
}
