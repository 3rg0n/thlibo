package processors

import (
	"testing"
)

// pytestFixtures are representative pytest outputs the filter must handle.
var pytestFixtures = map[string]string{
	"passing": `===== test session starts =====
platform linux -- Python 3.9.0, pytest-7.0.0
rootdir: /home/user/myproject
plugins: asyncio-0.18.0
collected 2 items

test_foo.py .. [100%]

===== 2 passed in 0.15s =====
`,
	"with_failures": `===== test session starts =====
platform linux -- Python 3.9.0
collected 3 items

test_foo.py .F. [100%]

===== FAILURES =====
___________ test_broken _____________

def test_broken():
>       assert False
E       AssertionError: assert False

test_foo.py:42: AssertionError

===== short test summary info =====
FAILED test_foo.py::test_broken - AssertionError: assert False
===== 1 failed, 2 passed in 0.22s =====
`,
	"with_errors": `===== test session starts =====
collected 2 items

test_bar.py .E [100%]

===== ERRORS =====
_____ ERROR at setup of test_needs_fixture ______

def my_fixture():
>       raise RuntimeError("fixture broken")

test_bar.py:5: RuntimeError

===== short test summary info =====
ERROR test_bar.py::test_needs_fixture
===== 1 error, 1 passed in 0.18s =====
`,
	"ansi_colored": `===== test session starts =====
collected 1 items

test_foo.py \x1b[32m.\x1b[0m [100%]

===== 1 passed in 0.10s =====
`,
	"non_pytest_noise": `Some random output that doesn't
look like pytest at all
Just plain text
`,
	"empty": "",
}

// TestPytestFilterParity runs the Go filter and the Python run.py on the
// same fixtures and requires byte-identical output (ADR 0010 parity).
// Skips when python3 isn't available.
func TestPytestFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available; parity check skipped")
	}
	script := referenceScript(t, "pytest-filter")
	for name, in := range pytestFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(pytestFilter([]byte(in)))
			if got != want {
				t.Errorf("pytest-filter parity mismatch on %q:\n got: %q\n want: %q", name, got, want)
			}
		})
	}
}
