package processors

import (
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
