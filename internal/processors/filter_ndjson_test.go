package processors

import (
	"strings"
	"testing"
)

// ndjsonFixtures are representative NDJSON outputs the filter must handle.
var ndjsonFixtures = map[string]string{
	"mixed_levels": `{"level":"error","msg":"connection refused","addr":"localhost:5000"}
{"level":"error","msg":"connection refused","addr":"localhost:5000"}
{"level":"warn","msg":"retrying","attempt":1}
{"level":"info","msg":"startup complete"}
`,
	"duplicates": `{"level":"error","msg":"disk full"}
{"level":"error","msg":"disk full"}
{"level":"error","msg":"disk full"}
{"level":"error","msg":"disk full"}
{"level":"error","msg":"disk full"}
`,
	"nested_fields": `{"severity":"ERROR","body":"auth failed","user":"alice","nested":{"code":"401"}}
{"severity":"ERROR","body":"auth failed","user":"bob","nested":{"code":"401"}}
{"loglevel":"INFO","text":"request","method":"GET"}
`,
	"non_json_noise": `{"level":"info","msg":"startup"}
not json at all
{"level":"error","msg":"crash"}
another garbage line
`,
	"empty": "",
	"only_noise": `garbage
not json
some random text
`,
	"otel_numeric": `{"severity":17,"body":"fatal error"}
{"severity":13,"body":"database error"}
{"severity":9,"body":"slow query"}
{"severity":5,"body":"info log"}
`,
	// #27: HTTP access logs (constant level+msg) must keep their
	// route/status distribution, not collapse to one row.
	"http_access": `{"level":"info","msg":"","RequestMethod":"POST","RequestPath":"/api/otel/v1/metrics","DownstreamStatus":400,"TraceId":"t-1","ClientHost":"10.0.0.1"}
{"level":"info","msg":"","RequestMethod":"POST","RequestPath":"/api/otel/v1/metrics","DownstreamStatus":400,"TraceId":"t-2","ClientHost":"10.0.0.2"}
{"level":"info","msg":"","RequestMethod":"GET","RequestPath":"/api/users/1007","DownstreamStatus":200,"TraceId":"t-3","ClientHost":"10.0.0.1"}
{"level":"info","msg":"","RequestMethod":"GET","RequestPath":"/api/users/2008","DownstreamStatus":200,"TraceId":"t-4","ClientHost":"10.0.0.3"}
{"level":"info","msg":"","RequestMethod":"GET","RequestPath":"/api/users/9999","DownstreamStatus":503,"TraceId":"t-5","ClientHost":"10.0.0.2"}
{"level":"info","msg":"","RequestMethod":"GET","RequestPath":"/healthz","DownstreamStatus":200,"TraceId":"t-6","ClientHost":"10.0.0.4"}
`,
}

// TestNdjsonHTTPAccessDistribution locks in the #27 fix with direct
// assertions (not just golden/parity): an access-log stream with a
// constant level+msg must preserve its route × status distribution
// instead of collapsing to a single row.
func TestNdjsonHTTPAccessDistribution(t *testing.T) {
	out := string(ndjsonFilter([]byte(ndjsonFixtures["http_access"])))
	lines := nonEmptyLines(out)

	// 6 input lines → 4 groups: metrics/4xx (×2), users/<var>/2xx (×2,
	// 1007+2008 collapse by path-shape), users/<var>/5xx (×1), healthz/2xx.
	if len(lines) != 4 {
		t.Fatalf("want 4 distinct groups, got %d:\n%s", len(lines), out)
	}
	// Every distinct route must survive.
	for _, want := range []string{"/api/otel/v1/metrics", "/api/users/", "/healthz"} {
		if !strings.Contains(out, want) {
			t.Errorf("route %q dropped from output:\n%s", want, out)
		}
	}
	// Both status outcomes present (the 200 and the 503 on /api/users).
	if !strings.Contains(out, "200") || !strings.Contains(out, "503") {
		t.Errorf("status distribution lost (need 200 and 503):\n%s", out)
	}
	// Counts must sum to the 6 input records (nothing dropped).
	total := 0
	for _, ln := range lines {
		total += countOf(ln)
	}
	if total != 6 {
		t.Errorf("record counts sum to %d, want 6 (data lost):\n%s", total, out)
	}
}

// TestNdjsonPathShapeCollapsesIDs: distinct high-cardinality path
// segments (numeric ids, uuids) on the same route+status collapse to one
// group; distinct routes stay separate.
func TestNdjsonPathShapeCollapsesIDs(t *testing.T) {
	in := `{"level":"info","msg":"","method":"GET","path":"/api/users/1007","status":200}
{"level":"info","msg":"","method":"GET","path":"/api/users/2008","status":200}
{"level":"info","msg":"","method":"GET","path":"/api/users/550e8400-e29b-41d4-a716-446655440000","status":200}
{"level":"info","msg":"","method":"GET","path":"/api/orders/5","status":200}
`
	out := string(ndjsonFilter([]byte(in)))
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("want 2 groups (users/<var>, orders/<var>), got %d:\n%s", len(lines), out)
	}
	// The users group collapsed all 3 ids (incl the uuid).
	var usersCount int
	for _, ln := range lines {
		if strings.Contains(ln, "/api/users/") {
			usersCount = countOf(ln)
		}
	}
	if usersCount != 3 {
		t.Errorf("/api/users ids should collapse to _count:3, got %d:\n%s", usersCount, out)
	}
}

// TestNdjsonGenericLogsUnchanged: a record with no HTTP fields must
// behave exactly as the old (level,msg) signature — the enrichment adds
// only empty components. Two same-(level,msg) records still collapse.
func TestNdjsonGenericLogsUnchanged(t *testing.T) {
	in := `{"level":"error","msg":"disk full","dev":"/sda1"}
{"level":"error","msg":"disk full","dev":"/sda2"}
{"level":"warn","msg":"retrying"}
`
	out := string(ndjsonFilter([]byte(in)))
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("generic logs: want 2 groups (disk full ×2, retrying), got %d:\n%s", len(lines), out)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

// countOf returns the _count value on an emitted line, or 1 if absent.
func countOf(line string) int {
	i := strings.Index(line, `"_count":`)
	if i < 0 {
		return 1
	}
	rest := line[i+len(`"_count":`):]
	n := 0
	for j := 0; j < len(rest) && rest[j] >= '0' && rest[j] <= '9'; j++ {
		n = n*10 + int(rest[j]-'0')
	}
	if n == 0 {
		return 1
	}
	return n
}

// TestNdjsonFilterParity runs the Go filter and the Python run.py on the
// same fixtures and requires byte-identical output (ADR 0010 parity).
// Skips when python3 isn't available.
func TestNdjsonFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available; parity check skipped")
	}
	script := referenceScript(t, "ndjson-filter")
	for name, in := range ndjsonFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(ndjsonFilter([]byte(in)))
			if got != want {
				t.Errorf("ndjson-filter parity mismatch on %q:\n got: %q\n want: %q", name, got, want)
			}
		})
	}
}
