package middleware

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/processors"
)

// --- overCollapsed heuristic (pure) ---

func TestOverCollapsed(t *testing.T) {
	// Helper: build an N-line ndjson-ish input and an M-line output.
	lines := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString("{\"x\":1}\n")
		}
		return b.String()
	}
	cases := []struct {
		name   string
		in     string
		out    string
		expect bool
	}{
		{"small input never fires", lines(10), "{}\n", false},
		{"collapsed to 1 row (300->1)", lines(300), "{}\n", true},
		{"collapsed to 2 rows", lines(300), "{}\n{}\n", true},
		{"3 rows but <5% ratio (300->3)", lines(300), "a\nb\nc\n", true},
		{"healthy ratio (300->60)", lines(300), lines(60), false},
		{"exactly at line floor, collapsed", lines(30), "{}\n", true},
		{"just under floor", lines(29), "{}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := overCollapsed(tc.in, tc.out); got != tc.expect {
				t.Errorf("overCollapsed = %v, want %v", got, tc.expect)
			}
		})
	}
}

// --- end-to-end: over-collapsed ndjson triggers the cordon fallback ---

// registryWithCordonStub builds a registry with the real embedded
// filters (incl. the native ndjson-filter) plus a user-dir cordon-filter
// script that shadows the real one with deterministic behaviour: it
// prints the fixed `stubOut` and exits 0 (no inferd needed).
func registryWithCordonStub(t *testing.T, stubOut string) *processors.Registry {
	t.Helper()
	userDir := t.TempDir()
	d := filepath.Join(userDir, "cordon-filter")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	var entry, body string
	if runtime.GOOS == "windows" {
		entry = "run.py"
		// Print stubOut verbatim (repr-safe via a here-ish approach).
		body = "import sys\nsys.stdout.write(" + pyQuote(stubOut) + ")\n"
	} else {
		entry = "run.sh"
		body = "#!/usr/bin/env bash\ncat >/dev/null\nprintf '%s' " + shQuote(stubOut) + "\n"
	}
	if err := os.WriteFile(filepath.Join(d, "processor.yaml"),
		[]byte("name: cordon-filter\ntype: script\nentry: "+entry+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, entry), []byte(body), 0o755); err != nil { // #nosec G306
		t.Fatal(err)
	}
	reg, _, err := BuildRegistry(userDir)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// accessLog builds n identical-signature ndjson access-log records (the
// #27 shape: constant level+msg, varying only in trace id) — ndjson-
// filter collapses these to ~1 row.
func accessLog(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(`{"level":"info","msg":"","RequestPath":"/api/x","DownstreamStatus":200,"TraceId":"t`)
		b.WriteString(itoa(i))
		b.WriteString(`"}` + "\n")
	}
	return b.String()
}

// TestCordonFallbackFiresOnOverCollapse: an access log that ndjson-filter
// collapses to ~1 row triggers the cordon fallback; when cordon returns a
// richer (more-lines, still-smaller) result, the pipeline prefers it.
func TestCordonFallbackFiresOnOverCollapse(t *testing.T) {
	requireShellForScripts(t)
	// cordon stub returns 5 "surfaced anomaly" lines — richer than
	// ndjson's 1-row collapse, and far smaller than the raw input.
	stub := "sig=anomaly-1\nsig=anomaly-2\nsig=anomaly-3\nsig=anomaly-4\nsig=anomaly-5\n"
	reg := registryWithCordonStub(t, stub)
	p := &Pipeline{Registry: reg, Dispatcher: &processors.Dispatcher{}}

	raw := accessLog(300)
	out := p.decide(context.Background(), raw)

	// Normalise CRLF: a script stub on Windows may emit \r\n. What matters
	// is that cordon's content (the anomaly lines), not ndjson's collapse,
	// was chosen.
	norm := strings.ReplaceAll(out, "\r\n", "\n")
	if norm != stub {
		t.Errorf("expected cordon output to be preferred over the over-collapsed ndjson output.\n got: %q", out)
	}
	if !strings.Contains(norm, "sig=anomaly-5") {
		t.Errorf("cordon anomaly output not surfaced:\n%s", out)
	}
}

// TestCordonFallbackKeptWhenNotBetter: if cordon fails open (returns the
// input verbatim, as it does when inferd's embed socket is down), the
// pipeline keeps ndjson's collapsed output — never the raw passthrough.
func TestCordonFallbackFailOpenKeepsNdjson(t *testing.T) {
	requireShellForScripts(t)
	raw := accessLog(300)
	// Stub cordon to echo the raw input back (fail-open behaviour).
	reg := registryWithCordonStub(t, raw)
	p := &Pipeline{Registry: reg, Dispatcher: &processors.Dispatcher{}}

	out := p.decide(context.Background(), raw)

	// Must NOT be the raw input (that would be a no-op / data-not-
	// compressed); must be ndjson's collapsed output instead.
	if out == raw {
		t.Errorf("cordon fail-open (verbatim) must not replace ndjson's output with the raw input")
	}
	if countNonEmptyLines(out) >= countNonEmptyLines(raw) {
		t.Errorf("expected ndjson's collapsed output (few lines), got %d lines", countNonEmptyLines(out))
	}
}

// TestCordonFallbackNotTriggeredForHealthyLog: a well-distributed ndjson
// stream (many distinct signatures) does NOT over-collapse, so cordon is
// never invoked — the ndjson output stands.
func TestCordonFallbackSkippedForHealthyLog(t *testing.T) {
	requireShellForScripts(t)
	// A cordon stub that, if wrongly invoked, would produce a detectable
	// marker — so we can assert it was NOT used.
	reg := registryWithCordonStub(t, "CORDON-WAS-WRONGLY-CALLED\n")
	p := &Pipeline{Registry: reg, Dispatcher: &processors.Dispatcher{}}

	// 300 records, each a DISTINCT path → ndjson keeps ~300 groups → not
	// over-collapsed.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString(`{"level":"info","msg":"","RequestPath":"/api/item/`)
		b.WriteString(itoa(i))
		b.WriteString(`","DownstreamStatus":200}` + "\n")
	}
	out := p.decide(context.Background(), b.String())

	if strings.Contains(out, "CORDON-WAS-WRONGLY-CALLED") {
		t.Errorf("cordon fallback fired on a healthy (well-distributed) log; it must only fire on over-collapse")
	}
}

// --- helpers ---

func requireShellForScripts(t *testing.T) {
	t.Helper()
	// The cordon stub is a script processor; on non-Windows it's bash, on
	// Windows it's python. The existing script-dispatch tests assume these
	// are present in CI. Skip only if neither is available.
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(os.Getenv("COMSPEC")); err != nil {
			// python is resolved by the dispatcher via PATH; can't cheaply
			// probe here, so just proceed — CI has python.
		}
	}
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func pyQuote(s string) string {
	// Produce a Python string literal.
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r")
	return "\"" + r.Replace(s) + "\""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
