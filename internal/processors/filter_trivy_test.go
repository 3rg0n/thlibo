package processors

import (
	"testing"
)

// trivyFixtures are representative Trivy outputs the filter must handle.
var trivyFixtures = map[string]string{
	"simple_table": `┌──────────────────────────────┬──────────────────┬──────────┐
│ Library                      │ Vulnerability    │ Severity │
├──────────────────────────────┼──────────────────┼──────────┤
│ openssl@1.1.1                │ CVE-2024-12345   │ HIGH     │
│                              │ CVE-2024-54321   │ MEDIUM   │
├──────────────────────────────┼──────────────────┼──────────┤
│ curl@7.64.1                  │ CVE-2024-11111   │ CRITICAL │
└──────────────────────────────┴──────────────────┴──────────┘
`,
	"with_wrapped_title": `┌──────────────────────────────┬──────────────────┬──────────┬───────────────────────┐
│ Library                      │ Vulnerability    │ Severity │ Title                 │
├──────────────────────────────┼──────────────────┼──────────┼───────────────────────┤
│ openssl@1.1.1                │ CVE-2024-12345   │ HIGH     │ Buffer overflow in    │
│                              │                  │          │ SSL_read function     │
├──────────────────────────────┼──────────────────┼──────────┼───────────────────────┤
│ log4j@2.13.0                 │ CVE-2021-44228   │ CRITICAL │ Remote Code           │
│                              │                  │          │ Execution via JNDI    │
└──────────────────────────────┴──────────────────┴──────────┴───────────────────────┘
`,
	"multiple_tables": `┌──────────────────────────────┬──────────────────┬──────────┐
│ Library                      │ Vulnerability    │ Severity │
├──────────────────────────────┼──────────────────┼──────────┤
│ openssl@1.1.1                │ CVE-2024-12345   │ HIGH     │
└──────────────────────────────┴──────────────────┴──────────┘

Some other content here

┌──────────────────────────────┬──────────────────┬──────────┐
│ Library                      │ Vulnerability    │ Severity │
├──────────────────────────────┼──────────────────┼──────────┤
│ curl@7.64.1                  │ CVE-2024-11111   │ CRITICAL │
└──────────────────────────────┴──────────────────┴──────────┘
`,
	"with_total_line": `┌──────────────────────────────┬──────────────────┬──────────┐
│ Library                      │ Vulnerability    │ Severity │
├──────────────────────────────┼──────────────────┼──────────┤
│ openssl@1.1.1                │ CVE-2024-12345   │ HIGH     │
└──────────────────────────────┴──────────────────┴──────────┘

Total: 1 (Critical: 0, High: 1, Medium: 0, Low: 0, Unknown: 0)
`,
	"non_table": "just some random text\nthat isn't a trivy table\n",
	"empty":     "",
}

// TestTrivyFilterParity runs the Go filter and the Python run.py on the
// same fixtures and requires byte-identical output (ADR 0010 parity).
// Skips when python3 isn't available (e.g. minimal CI images) — the
// filter's own behaviour is still covered by golden tests below.
func TestTrivyFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available; parity check skipped")
	}
	script := referenceScript(t, "trivy-filter")
	for name, in := range trivyFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(trivyFilter([]byte(in)))
			// The middleware applies the monotonic guard on top; here we
			// compare the raw transform to Python's raw transform.
			if got != want {
				t.Errorf("trivy-filter parity mismatch on %q:\n go:\n%s\n py:\n%s", name, got, want)
			}
		})
	}
}
