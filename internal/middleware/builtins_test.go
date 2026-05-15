package middleware

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	builtins "github.com/3rg0n/thlibo/processors"

	"github.com/3rg0n/thlibo/internal/processors"
)

// C4: with no user dir, the registry is populated from the embedded
// built-ins. Update this list whenever a new built-in lands in
// processors/ — keep it sorted to spot drift instantly.
//
// v0.1: cargo-filter, casefolder, compress, git-filter, npm-filter
// v0.4: + shorthand
// v0.5: + stacktrace-filter, pytest-filter, ndjson-filter
func TestBuiltinsLoadedWithNoUserDir(t *testing.T) {
	reg, warnings, err := BuildRegistry("")
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	want := []string{
		"cargo-filter", "casefolder", "compress", "git-filter",
		"ndjson-filter", "npm-filter", "pytest-filter", "shorthand",
		"stacktrace-filter",
	}
	for _, n := range want {
		if reg.Get(n) == nil {
			t.Errorf("builtin %q missing from registry", n)
		}
	}
	if reg.Len() != len(want) {
		t.Errorf("registry has %d processors (%v), want %d", reg.Len(), reg.Names(), len(want))
	}
}

// C4: missing user dir doesn't fail the build, built-ins still load.
func TestBuiltinsLoadedWithMissingUserDir(t *testing.T) {
	reg, warnings, err := BuildRegistry("/path/that/definitely/does/not/exist/here")
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	// Count must match the embedded set; see the named list in
	// TestBuiltinsLoadedWithNoUserDir for what's expected.
	if reg.Len() != 9 {
		t.Errorf("registry has %d processors, want 9", reg.Len())
	}
}

// C5 at the middleware level: a user processor with the same name as
// a built-in overrides it. End-to-end through BuildRegistry so we
// exercise the real embed -> user merge path.
func TestUserOverridesBuiltinViaBuildRegistry(t *testing.T) {
	dir := t.TempDir()
	compressDir := filepath.Join(dir, "compress")
	if err := os.MkdirAll(compressDir, 0o755); err != nil {
		t.Fatal(err)
	}
	override := `---
name: compress
type: prompt
description: "user override"
---
user override body
`
	if err := os.WriteFile(filepath.Join(compressDir, "processor.md"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, _, err := BuildRegistry(dir)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	d := reg.Get("compress")
	if d == nil {
		t.Fatal("compress not loaded")
	}
	if d.Origin.Source != processors.OriginUser {
		t.Errorf("compress origin = %v, want user", d.Origin.Source)
	}
	if !strings.Contains(d.SystemPrompt, "user override body") {
		t.Errorf("user prompt not used; SystemPrompt = %q", d.SystemPrompt)
	}
}

// C6: each built-in script processor produces non-empty,
// non-identical, shorter output on its representative fixture. Prompt
// processors are verified by descriptor sanity only; runtime quality
// is F6 territory (Phase 6).
func TestScriptBuiltinsC6(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH; C6 for script built-ins requires it (documented in README)")
	}

	// The dispatcher chdirs + execs entry files, so we materialise
	// the embedded FS to a temp dir first (this is what `thlibo
	// install` will do in Phase 6).
	diskRoot := t.TempDir()
	if err := copyEmbedTree(builtins.FS, ".", diskRoot); err != nil {
		t.Fatalf("mirror builtins: %v", err)
	}
	reg, warnings, err := processors.BuildFromDisk(diskRoot, "")
	if err != nil {
		t.Fatalf("build from mirrored dir: %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("mirrored-registry warnings: %v", warnings)
	}

	disp := &processors.Dispatcher{}

	cases := []struct {
		name    string
		fixture string
	}{
		{"git-filter", gitStatusFixture},
		{"npm-filter", npmListFixture},
		{"cargo-filter", cargoBuildFixture},
		{"stacktrace-filter", pythonRecursionFixture},
		{"pytest-filter", pytestSessionFixture},
		{"ndjson-filter", ndjsonRepeatedErrorFixture},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := reg.Get(c.name)
			if d == nil {
				t.Fatalf("%s not in registry (names: %v)", c.name, reg.Names())
			}
			got, err := disp.Run(context.Background(), d, c.fixture)
			if err != nil {
				t.Fatalf("%s run: %v", c.name, err)
			}
			if strings.TrimSpace(got) == "" {
				t.Errorf("%s produced empty output", c.name)
			}
			if got == c.fixture {
				t.Errorf("%s returned input unchanged; compression not applied", c.name)
			}
			if len(got) >= len(c.fixture) {
				t.Errorf("%s produced %d bytes from %d-byte input; expected compression",
					c.name, len(got), len(c.fixture))
			}
		})
	}
}

// C6 for prompt builtins: descriptor shape check, no daemon round-trip
// (that's Phase 6 F6 territory).
func TestPromptBuiltinsDescriptorsSane(t *testing.T) {
	reg, _, err := BuildRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"compress", "casefolder"} {
		d := reg.Get(name)
		if d == nil {
			t.Errorf("%s missing", name)
			continue
		}
		if d.Type != processors.KindPrompt {
			t.Errorf("%s type = %v, want prompt", name, d.Type)
		}
		if strings.TrimSpace(d.SystemPrompt) == "" {
			t.Errorf("%s has empty system prompt", name)
		}
		if d.Description == "" {
			t.Errorf("%s has no description", name)
		}
	}
}

// copyEmbedTree copies src's tree (rooted at srcRoot within the
// embed.FS) onto disk at destRoot, preserving directory structure.
func copyEmbedTree(src fs.FS, srcRoot, destRoot string) error {
	return fs.WalkDir(src, srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := p
		if srcRoot != "." {
			rel = strings.TrimPrefix(p, srcRoot+"/")
		}
		target := filepath.Join(destRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o755)
	})
}

// ---- fixtures ------------------------------------------------------

const gitStatusFixture = `On branch main
Your branch is up to date with 'origin/main'.

Changes to be committed:
  (use "git restore --staged <file>..." to unstage)
	modified:   cmd/thlibo/main.go
	new file:   internal/middleware/middleware.go

Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)
  (use "git restore <file>..." to discard changes in working directory)
	modified:   README.md

Untracked files:
  (use "git add <file>..." to include in what will be committed)
	.plan/notes.md
	scratch.txt

diff --git a/README.md b/README.md
index 1111111..2222222 100644
--- a/README.md
+++ b/README.md
@@ -1,4 +1,4 @@
-# thlibo
+# Thlibo

 A programmatic inference daemon.

@@ -10,3 +10,10 @@ A programmatic inference daemon.
 ## Install

 TBD
+
+## New section
+
+content
+more content
+
`

const npmListFixture = `npm@10.2.4 /usr/local/lib
├── @anthropic-ai/sdk@0.24.3
├─┬ @typescript-eslint/eslint-plugin@6.21.0
│ ├─┬ @eslint-community/regexpp@4.10.0
│ ├── @typescript-eslint/parser@6.21.0 deduped
│ ├── @typescript-eslint/scope-manager@6.21.0
│ ├── @typescript-eslint/type-utils@6.21.0
│ ├── @typescript-eslint/utils@6.21.0
│ ├── debug@4.3.4
│ ├── graphemer@1.4.0
│ ├── ignore@5.3.1
│ ├── natural-compare@1.4.0
│ ├── semver@7.5.4 deduped
│ └── ts-api-utils@1.3.0
├── eslint@8.56.0
├─┬ lodash@4.17.21
├── prettier@3.2.5
├── typescript@5.3.3
└── zod@3.22.4

added 142 packages, and audited 143 packages in 4s

38 packages are looking for funding
  run ` + "`" + `npm fund` + "`" + ` for details

found 0 vulnerabilities
`

const cargoBuildFixture = `    Updating crates.io index
    Downloaded serde_json v1.0.108
    Downloaded 1 crate (143.3 KB) in 0.45s
   Compiling proc-macro2 v1.0.69
   Compiling unicode-ident v1.0.12
   Compiling quote v1.0.33
   Compiling syn v2.0.39
   Compiling serde_derive v1.0.190
   Compiling serde v1.0.190
   Compiling serde_json v1.0.108
   Compiling thlibo v0.1.0 (/repo/thlibo)
error[E0308]: mismatched types
  --> src/main.rs:42:18
   |
42 |     let x: u32 = "hello";
   |            ---   ^^^^^^^ expected ` + "`" + `u32` + "`" + `, found ` + "`" + `&str` + "`" + `
   |            |
   |            expected due to this

warning: unused variable: ` + "`" + `y` + "`" + `
  --> src/main.rs:43:9
   |
43 |     let y = 5;
   |         ^ help: if this is intentional, prefix it with an underscore: ` + "`" + `_y` + "`" + `
   |
   = note: ` + "`" + `#[warn(unused_variables)]` + "`" + ` on by default

error: could not compile ` + "`" + `thlibo` + "`" + ` (bin "thlibo") due to 1 previous error
`

// Pytest session with one failure. The filter should drop the
// env-info block, drop the per-file dot-progress lines, and keep
// the FAILURES + short summary blocks intact.
const pytestSessionFixture = `============================= test session starts ==============================
platform linux -- Python 3.12.3, pytest-8.0.0, pluggy-1.4.0
rootdir: /home/user/repo
configfile: pyproject.toml
plugins: anyio-4.0.0, asyncio-0.21.1
collected 47 items

tests/test_a.py ........                                                 [ 17%]
tests/test_b.py ......F.                                                 [ 34%]
tests/test_c.py ............                                             [ 60%]
tests/test_d.py ............                                             [ 85%]
tests/test_e.py .......                                                  [100%]

=================================== FAILURES ===================================
__________________________________ test_login _________________________________

    def test_login():
        result = login("user", "pass")
>       assert result.status == 200
E       assert 401 == 200
E        +  where 401 = LoginResult(status=401).status

tests/test_b.py:42: AssertionError
=========================== short test summary info ============================
FAILED tests/test_b.py::test_login - assert 401 == 200
========================= 1 failed, 46 passed in 1.23s =========================
`

// 50 NDJSON records with one error repeated 47 times. Filter
// should dedupe the duplicates with a _count multiplier and sort
// errors first.
const ndjsonRepeatedErrorFixture = `{"level":"info","msg":"service started","version":"v0.5.0","pid":12345}
{"level":"warn","msg":"slow query","ms":230,"query":"SELECT * FROM users"}
{"level":"warn","msg":"deprecated API","endpoint":"/v1/auth","callsite":"auth.go:42"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-0"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-1"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-2"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-3"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-4"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-5"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-6"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-7"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-8"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-9"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-10"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-11"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-12"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-13"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-14"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-15"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-16"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-17"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-18"}
{"level":"error","msg":"connection refused","host":"db1.example.com:5432","request_id":"req-19"}
`

// 50-deep recursion-error trace. The filter should produce a
// shorter output by dedupe + head/tail elision while preserving
// the exception class, message, file, and line number.
const pythonRecursionFixture = `Traceback (most recent call last):
  File "/app/x.py", line 12, in <module>
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    f()
  File "/app/x.py", line 5, in f
    raise RecursionError("max depth")
RecursionError: maximum recursion depth exceeded
`
