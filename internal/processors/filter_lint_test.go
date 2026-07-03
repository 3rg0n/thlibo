package processors

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

var lintFixtures = map[string]string{
	"gcc_basic": `src/test.c:1:9: warning: unused variable 'x' [-Wunused-variable]
src/test.c:2:9: warning: unused variable 'y' [-Wunused-variable]
src/test.c:3:9: warning: unused variable 'z' [-Wunused-variable]
`,
	"shellcheck": `scripts/a.sh:5:12: warning: Quote this to prevent word splitting. [SC2086]
scripts/b.sh:5:12: warning: Quote this to prevent word splitting. [SC2086]
scripts/c.sh:5:12: warning: Quote this to prevent word splitting. [SC2086]
`,
	"empty": "",
	"non_lint": `Hello
This is not lint output
`,
	"rubocop": `app/models/user.rb:10:5: C: [Correctable] Style/StringLiterals: Prefer single-quoted strings.
app/models/user.rb:12:80: C: Layout/LineLength: Line is too long. [95/80]
`,
	"eslint_compact": `src/app.js: line 1, col 1, Error - Unexpected console statement. (no-console)
src/app.js: line 5, col 3, Warning - Missing semicolon. (semi)
`,
	"eslint_unix": `src/app.js:1:1: Unexpected console statement. [Error/no-console]
src/app.js:5:3: Missing semicolon. [Warning/semi]
`,
	"golangci": `internal/foo.go:12:2: ineffectual assignment to err (ineffassign)
internal/foo.go:40:6: exported func Bar should have comment (golint)
`,
	"gosec": `[internal/x.go:42] - G404 (CWE-338): Use of weak random number generator (Confidence: HIGH, Severity: HIGH)
[internal/y.go:7] - G104 (CWE-703): Errors unhandled (Confidence: HIGH, Severity: LOW)
`,
	"flake_ruff": `src/x.py:1:1: F401 'os' imported but unused
src/x.py:2:1: E302 expected 2 blank lines
src/x.py:3:5: E225 missing whitespace around operator
`,
	"mypy": `src/x.py:10: error: Incompatible return value type [return-value]
src/x.py:12: note: Revealed type is "builtins.int"
`,
	"tsc": `src/app.ts(10,5): error TS2322: Type 'string' is not assignable to type 'number'.
src/app.ts(20,3): warning TS6133: 'x' is declared but never used.
`,
	"stylelint": `styles/a.css:3:5: warning  Expected indentation of 2 spaces [indentation]
`,
	"rustc_verbose": `warning: unused variable: ` + "`x`" + `
 --> src/main.rs:2:9
  |
2 |     let x = 5;
  |         ^ help: if this is intentional, prefix it with an underscore: ` + "`_x`" + `
  |
  = note: ` + "`#[warn(unused_variables)]`" + ` on by default

error[E0382]: borrow of moved value
 --> src/main.rs:10:20
  |
9 |     let s = String::new();
  |
`,
	"ruff_verbose": `F401 [*] ` + "`os`" + ` imported but unused
 --> src/x.py:1:8
  |
1 | import os
  |        ^^
  = help: Remove unused import

E501 Line too long
 --> src/x.py:5:80
`,
	"eslint_stylish": `/project/src/app.js
   1:1   error    Unexpected console statement  no-console
   5:3   warning  Missing semicolon             semi
`,
	"gcc_verbose": `src/test.c:5:9: warning: unused variable 'x' [-Wunused-variable]
    5 |     int x = 0;
      |         ^
src/test.c:10:5: error: 'y' undeclared [-Werror]
   10 |     y = 1;
      |     ^
`,
	"mixed": `src/x.py:1:1: F401 'os' imported but unused
scripts/a.sh:5:12: warning: Quote this. [SC2086]
[internal/x.go:42] - G404 (CWE-338): weak rng (Confidence: HIGH, Severity: HIGH)
`,
}

func TestLintFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available; parity check skipped")
	}
	script := referenceScript(t, "lint-filter")
	for name, in := range lintFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(lintFilter([]byte(in)))
			if got != want {
				t.Errorf("lint-filter parity mismatch on %q:\ngot:\n%q\nwant:\n%q",
					name, got, want)
			}
		})
	}
}

func TestLintFilterGolden(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty_passthrough",
			in:   "",
			want: "",
		},
		{
			name: "non_lint_passthrough",
			in:   "Hello\nWorld\n",
			want: "Hello\nWorld\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(lintFilter([]byte(tt.in)))
			if got != tt.want {
				t.Errorf("lint-filter:\ngot:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestLintFilterMaxPerRule(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available")
	}
	script := referenceScript(t, "lint-filter")

	in := `src/x.py:1:1: E302 blank lines
src/x.py:2:1: E302 blank lines
src/x.py:3:1: E302 blank lines
src/x.py:4:1: E302 blank lines
src/x.py:5:1: E302 blank lines
`

	oldMax := os.Getenv("LINT_MAX_PER_RULE")
	defer func() {
		if oldMax != "" {
			os.Setenv("LINT_MAX_PER_RULE", oldMax)
		} else {
			os.Unsetenv("LINT_MAX_PER_RULE")
		}
	}()

	cmd := exec.Command(py, script)
	cmd.Stdin = strings.NewReader(in)
	cmd.Env = append(os.Environ(), "LINT_MAX_PER_RULE=2")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python %s failed: %v", script, err)
	}
	want := string(out)

	os.Setenv("LINT_MAX_PER_RULE", "2")
	got := string(lintFilter([]byte(in)))
	if got != want {
		t.Errorf("lint-filter with MAX_PER_RULE=2:\ngot:\n%q\nwant:\n%q", got, want)
	}
}
