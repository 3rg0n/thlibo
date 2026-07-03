package processors

import (
	"testing"
)

var goTestFixtures = map[string]string{
	"all_pass": `=== RUN   TestA
--- PASS: TestA (0.00s)
=== RUN   TestB
--- PASS: TestB (0.00s)
PASS
ok  	github.com/x/y	0.042s
`,
	"single_failure": `=== RUN   TestPass
--- PASS: TestPass (0.00s)
=== RUN   TestFail
    foo_test.go:42: got 7, want 9
--- FAIL: TestFail (0.00s)
FAIL
FAIL	github.com/x/y	0.05s
`,
	"build_error": `# github.com/x/y
./broken.go:10:2: undefined: fmt.Printlnx
FAIL	github.com/x/y [build failed]
`,
	"mixed_packages": `=== RUN   TestA
--- PASS: TestA (0.00s)
ok  	github.com/x/a	0.01s
=== RUN   TestB
    b_test.go:5: boom
--- FAIL: TestB (0.00s)
FAIL
FAIL	github.com/x/b	0.02s
?   	github.com/x/c	[no test files]
`,
	"panic": `=== RUN   TestPanics
panic: runtime error: index out of range [3] with length 2

goroutine 6 [running]:
github.com/x/y.TestPanics(0x...)
	/src/y_test.go:14 +0x1d
FAIL	github.com/x/y	0.03s
`,
	"json_events": `{"Action":"run","Test":"TestPass0"}
{"Action":"output","Test":"TestPass0","Output":"=== RUN   TestPass0\n"}
{"Action":"output","Test":"TestPass0","Output":"--- PASS: TestPass0 (0.00s)\n"}
{"Action":"pass","Test":"TestPass0"}
{"Action":"run","Test":"TestB"}
{"Action":"output","Test":"TestB","Output":"    b_test.go:5: got X want Y\n"}
{"Action":"fail","Test":"TestB"}
{"Action":"fail","Package":"github.com/x/y"}
`,
	"skip": `=== RUN   TestA
--- PASS: TestA (0.00s)
=== RUN   TestSkip1
--- SKIP: TestSkip1 (0.00s)
=== RUN   TestSkip2
--- SKIP: TestSkip2 (0.00s)
ok  	github.com/x/y	0.01s
`,
	"non_gotest": `just some
random text
not go test at all
`,
	"empty": "",
}

func TestGoTestFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available; parity check skipped")
	}
	script := referenceScript(t, "go-test-filter")
	for name, in := range goTestFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(goTestFilter([]byte(in)))
			if got != want {
				t.Errorf("go-test-filter parity mismatch on %q:\ngot:\n%q\nwant:\n%q",
					name, got, want)
			}
		})
	}
}

func TestGoTestFilterGolden(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "drops_pass_noise",
			in: `=== RUN   TestA
--- PASS: TestA (0.00s)
PASS
ok  	github.com/x/y	0.01s
`,
			want: `ok  	github.com/x/y	0.01s
`,
		},
		{
			name: "keeps_fail_detail",
			in: `=== RUN   TestFail
    x.go:1: error message
--- FAIL: TestFail (0.00s)
FAIL
FAIL	github.com/x/y	0.01s
`,
			want: `    x.go:1: error message
--- FAIL: TestFail (0.00s)
FAIL
FAIL	github.com/x/y	0.01s
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(goTestFilter([]byte(tt.in)))
			if got != tt.want {
				t.Errorf("go-test-filter:\ngot:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}
