package processors

import (
	"testing"
)

// cargoFixtures are representative cargo outputs the filter must handle.
var cargoFixtures = map[string]string{
	"build_error":    "   Compiling mylib v0.1.0\nerror[E0382]: borrow of moved value\n  --> src/lib.rs:10:13\nerror: aborting\nFinished dev [unoptimized] target(s) in 0.52s\n",
	"test_output":    "running 5 tests\ntest tests::test_parse ... ok\ntest tests::test_edge_case ... FAILED\ntest result: FAILED. 4 passed; 1 failed\nFinished test [unoptimized] target(s) in 1.23s\n",
	"warning":        "   Compiling app v1.0.0\nwarning: unused variable\n  --> src/main.rs:5:9\nwarning: 1 warning emitted\nFinished release [optimized] target(s) in 3.45s\n",
	"simple_success": "   Compiling app v0.1.0\nFinished release [optimized] target(s) in 5.23s\n",
	"empty":          "",
}

// TestCargoFilterParity runs the Go filter and the Python run.py on the
// same fixtures and requires byte-identical output (ADR 0010 parity).
func TestCargoFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available; parity check skipped")
	}
	script := referenceScript(t, "cargo-filter")
	for name, in := range cargoFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(cargoFilter([]byte(in)))
			if got != want {
				t.Errorf("cargo-filter parity mismatch on %q:\n go: %q\n py: %q", name, got, want)
			}
		})
	}
}
