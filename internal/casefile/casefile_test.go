package casefile

import (
	"bytes"
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

// TestStripSentinelLine: the helper deletes any line containing the
// pdf-to-md low-value marker, leaves the rest of the buffer alone,
// and is a no-op when the marker is absent. Trailing newline shape
// follows what bytes.Split + bytes.Join produces; we don't trim it
// because compressed.log keeps its trailing newline.
func TestStripSentinelLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// preserved is content the strip operation must leave intact.
		// We don't pin exact whitespace because bytes.Split/Join
		// handles trailing newlines in a way that doesn't matter for
		// the consumer (compressed.log gets read whole).
		preserved []string
	}{
		{
			name:      "marker as the only line",
			in:        "<!-- thlibo-pdf-low-value: no extractable text -->\n",
			preserved: nil,
		},
		{
			name:      "marker on its own line after content",
			in:        "## Page 1\n\n_[scanned]_\n<!-- thlibo-pdf-low-value: x -->\n",
			preserved: []string{"## Page 1", "_[scanned]_"},
		},
		{
			name:      "no marker at all is a no-op",
			in:        "regular content\nwith newlines\n",
			preserved: []string{"regular content", "with newlines"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripSentinelLine([]byte(tc.in))
			if strings.Contains(string(got), lowValueSentinel) {
				t.Errorf("output still contains marker: %q", got)
			}
			for _, want := range tc.preserved {
				if !strings.Contains(string(got), want) {
					t.Errorf("expected %q to survive strip; got %q", want, got)
				}
			}
			// No-marker case must round-trip byte-for-byte.
			if !strings.Contains(tc.in, lowValueSentinel) && string(got) != tc.in {
				t.Errorf("no-marker input must be unchanged:\n  in:  %q\n  out: %q", tc.in, got)
			}
		})
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

// A binary source's byte count isn't comparable to extracted text, so the
// reduction figure must be labelled rather than presented as a saving.
// This is the metric that let pdf-to-md's slide-deck inflation read as an
// 85% win (fixed in v0.11.2) — the number was right, the basis was wrong.
func TestReductionBasis(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"plain text", []byte("On branch main\nnothing to commit\n"), BasisBytes},
		{"utf-8 text with accents", []byte("café — naïve\n"), BasisBytes},
		{"pdf header + NUL", append([]byte("%PDF-1.7\n"), 0x00, 'x'), BasisIncomparable},
		{"empty", nil, BasisBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BasisBytes
			if binarySource(tc.raw) {
				got = BasisIncomparable
			}
			if got != tc.want {
				t.Errorf("basis = %q, want %q", got, tc.want)
			}
		})
	}
}

// A NUL beyond the 8 KiB sniff window is not detected — documented
// conservatism, not an oversight. A missed binary verdict leaves the
// figure as misleading as it was before; it never breaks a case.
func TestBinarySourceSniffsHeadOnly(t *testing.T) {
	raw := append(bytes.Repeat([]byte("a"), 9000), 0x00)
	if binarySource(raw) {
		t.Error("sniff window should be bounded to the first 8 KiB")
	}
	if !binarySource(append(bytes.Repeat([]byte("a"), 100), 0x00)) {
		t.Error("a NUL inside the window must be detected")
	}
}

// The summary must carry the caveat, since that's where a human reads the
// number. meta.json carries the machine-readable basis.
func TestSummaryLabelsIncomparableReduction(t *testing.T) {
	dir := t.TempDir()
	meta := Meta{
		ID: "x", SourcePath: "a.pdf", SourceSize: 1000, CompressedSize: 150,
		ReductionPercent: 85.0, ReductionBasis: BasisIncomparable,
		CreatedAt: time.Unix(0, 0).UTC(),
	}
	if err := writeSummary(dir, meta); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "not a token saving") {
		t.Errorf("summary must caveat an incomparable reduction:\n%s", got)
	}

	meta.ReductionBasis = BasisBytes
	if err := writeSummary(dir, meta); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "summary.md"))
	if strings.Contains(string(got), "not a token saving") {
		t.Errorf("a text source must not be caveated:\n%s", got)
	}
	if !strings.Contains(string(got), "reduction: 85.00%") {
		t.Errorf("summary lost the reduction line:\n%s", got)
	}
}
