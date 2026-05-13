package middleware

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	builtins "github.com/3rg0n/thlibo/processors"

	"github.com/3rg0n/thlibo/internal/processors"
	"github.com/3rg0n/thlibo/internal/router"
)

// TestTokenSavingsTable satisfies gate row F6. For each built-in
// script processor we have a representative fixture, runs it
// through the same dispatcher thlibo uses at runtime, and records
// before/after byte counts. The spec's §"Token savings estimate"
// lists target ratios per processor type; we don't hard-fail on
// target miss (per the amended F6 gate row), but we record the
// numbers so release notes can publish them.
//
// Prompt processors (compress, casefolder) are not exercised here
// because they need a live daemon. Their F6 numbers come from the
// release-machine smoke test documented in the release procedure.
//
// On failure or suspiciously-unchanged output, the test dumps what
// it saw so a regression stands out in CI logs.
func TestTokenSavingsTable(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH; script-processor savings measurement needs python")
	}

	// Mirror the embedded FS to disk so the script dispatcher can
	// chdir+exec entry files.
	diskRoot := t.TempDir()
	if err := mirrorEmbedForSavings(diskRoot); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	reg, warnings, err := processors.BuildFromDisk(diskRoot, "")
	if err != nil || len(warnings) > 0 {
		t.Fatalf("build registry: err=%v warnings=%v", err, warnings)
	}

	disp := &processors.Dispatcher{}

	// Fixtures must exceed the middleware's 2000-byte short-circuit
	// to exercise the dispatch path. Fixtures under that threshold
	// are explicitly designed to pass through unchanged (gate B1)
	// and measuring their "savings" would just confirm our
	// short-circuit works, which is a different row (B1).
	cases := []savingsCase{
		{"git-filter", "git status 50-file working tree", gitStatusLargeFixture()},
		{"git-filter", "git diff HEAD~N", gitDiffFixture()},
		{"npm-filter", "npm list 200 deps", npmListLargeFixture()},
		{"cargo-filter", "cargo test with 20 failures", cargoTestLargeFixture()},
	}

	var report strings.Builder
	fmt.Fprintln(&report)
	fmt.Fprintln(&report, "| processor | fixture | raw bytes | compressed bytes | reduction |")
	fmt.Fprintln(&report, "|---|---|---:|---:|---:|")

	for _, c := range cases {
		d := reg.Get(c.processor)
		if d == nil {
			t.Errorf("%s not in registry", c.processor)
			continue
		}
		// Wrap in a non-routing pipeline so we exercise the same
		// code path Claude Code users hit: fast-path match →
		// script dispatch → compressed stdout.
		p := &Pipeline{
			Registry:   reg,
			Router:     &forceRoute{chain: []string{c.processor}},
			Dispatcher: disp,
		}
		var out bytes.Buffer
		if err := p.Process(context.Background(), strings.NewReader(c.input), &out); err != nil {
			t.Errorf("%s/%s: %v", c.processor, c.fixture, err)
			continue
		}
		raw := len(c.input)
		compressed := out.Len()
		if compressed == 0 {
			t.Errorf("%s/%s: compressed output is empty", c.processor, c.fixture)
			continue
		}
		if compressed >= raw {
			t.Errorf("%s/%s: no compression (raw=%d compressed=%d)",
				c.processor, c.fixture, raw, compressed)
		}
		pct := 100.0 - (float64(compressed)*100.0)/float64(raw)
		fmt.Fprintf(&report, "| %s | %s | %d | %d | %.1f%% |\n",
			c.processor, c.fixture, raw, compressed, pct)
	}

	// Always log the report — the test is "passing" as long as
	// each row had >0 compression, but the human reading CI logs
	// wants to see the numbers.
	t.Log(report.String())

	// Also write to a file inside the test tmp dir so an operator
	// running the suite locally can recover the numbers for release
	// notes without re-running.
	outPath := filepath.Join(t.TempDir(), "f6-savings-report.md")
	_ = os.WriteFile(outPath, []byte(report.String()), 0o600)
	t.Logf("savings report: %s", outPath)
}

type savingsCase struct {
	processor string
	fixture   string
	input     string
}

// forceRoute is a RouterClient that always returns a specific chain,
// so the savings test isn't gated on a running daemon. Fast-path
// matching on the script processors' `match` regex would also work
// for git-filter but not reliably for npm/cargo fixtures; forcing
// the chain gives deterministic, reproducible numbers.
type forceRoute struct {
	chain []string
}

func (f *forceRoute) Ask(_ context.Context, _ *processors.Registry, _ string) (router.Decision, error) {
	return router.Decision{Chain: f.chain}, nil
}

// gitStatusLargeFixture is a status output for a 50-file working
// tree — large enough to exceed the short-circuit and show how
// much the hint-line + separator compression saves.
func gitStatusLargeFixture() string {
	var b strings.Builder
	b.WriteString("On branch main\n")
	b.WriteString("Your branch is up to date with 'origin/main'.\n\n")
	b.WriteString("Changes to be committed:\n")
	b.WriteString("  (use \"git restore --staged <file>...\" to unstage)\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "\tmodified:   internal/pkg/subpkg/very/deep/path/to/file_%02d.go\n", i)
	}
	b.WriteString("\nChanges not staged for commit:\n")
	b.WriteString("  (use \"git add <file>...\" to update what will be committed)\n")
	b.WriteString("  (use \"git restore <file>...\" to discard changes in working directory)\n")
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&b, "\tmodified:   cmd/subcmd/another/nested/dir/module_%02d.go\n", i)
	}
	b.WriteString("\nUntracked files:\n")
	b.WriteString("  (use \"git add <file>...\" to include in what will be committed)\n")
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&b, "\ttests/fixtures/generated/sample_%02d.txt\n", i)
	}
	b.WriteString("\nno changes added to commit (use \"git add\" and/or \"git commit -a\")\n")
	return b.String()
}

// npmListLargeFixture is a tree-style `npm list` for ~200 deps,
// the same shape as the spec's token-savings table example.
func npmListLargeFixture() string {
	var b strings.Builder
	b.WriteString("myapp@1.0.0 /repo/myapp\n")
	topLevel := []string{
		"@anthropic-ai/sdk", "@typescript-eslint/eslint-plugin",
		"@typescript-eslint/parser", "eslint", "prettier", "typescript",
		"zod", "lodash", "react", "react-dom", "next", "axios",
		"express", "socket.io", "jest", "vitest", "playwright",
		"commander", "chalk", "dotenv",
	}
	for i, pkg := range topLevel {
		prefix := "├─┬"
		if i == len(topLevel)-1 {
			prefix = "└─┬"
		}
		fmt.Fprintf(&b, "%s %s@%d.%d.%d\n", prefix, pkg, i+1, i, i%10)
		for j := 0; j < 9; j++ {
			b.WriteString("│ ├── ")
			fmt.Fprintf(&b, "transitive-dep-%s-%d@%d.%d.%d\n", pkg, j, j+1, j%5, (j*3)%10)
		}
	}
	b.WriteString("\nadded 217 packages, and audited 218 packages in 6s\n")
	b.WriteString("\n42 packages are looking for funding\n")
	b.WriteString("  run `npm fund` for details\n\n")
	b.WriteString("found 0 vulnerabilities\n")
	return b.String()
}

// cargoTestLargeFixture: a test run with multiple failures plus the
// per-crate compilation chatter we expect cargo-filter to strip.
func cargoTestLargeFixture() string {
	var b strings.Builder
	b.WriteString("    Updating crates.io index\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "    Downloaded transitive-crate-%02d v0.%d.%d\n", i, i%10, (i*3)%100)
	}
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "   Compiling some-crate-%02d v1.%d.%d\n", i, i, i%20)
	}
	b.WriteString("   Compiling myapp v0.1.0 (/repo/myapp)\n")
	b.WriteString("    Finished `test` profile [unoptimized + debuginfo] target(s) in 12.47s\n")
	b.WriteString("     Running unittests src/lib.rs (target/debug/deps/myapp-abc123)\n")
	b.WriteString("\nrunning 120 tests\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "test module::submodule::test_case_%03d ... ok\n", i)
	}
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "test module::submodule::test_failing_case_%02d ... FAILED\n", i)
	}
	b.WriteString("\nfailures:\n\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "---- module::submodule::test_failing_case_%02d stdout ----\n", i)
		b.WriteString("thread panicked at src/lib.rs:42:9:\n")
		fmt.Fprintf(&b, "assertion `left == right` failed\n  left: %d\n  right: %d\n", i, i*2)
		b.WriteString("note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n\n")
	}
	b.WriteString("test result: FAILED. 100 passed; 20 failed; 0 ignored; 0 measured; 0 filtered out\n")
	b.WriteString("\nerror: test failed, to rerun pass `--lib`\n")
	return b.String()
}

// gitDiffFixture is a large synthetic diff that exercises the
// hunk-dropping logic without depending on actual repo state.
func gitDiffFixture() string {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "diff --git a/file_%d.go b/file_%d.go\n", i, i)
		fmt.Fprintf(&b, "index abc%03d..def%03d 100644\n", i, i)
		fmt.Fprintf(&b, "--- a/file_%d.go\n", i)
		fmt.Fprintf(&b, "+++ b/file_%d.go\n", i)
		fmt.Fprintf(&b, "@@ -1,10 +1,12 @@\n")
		b.WriteString(" package foo\n\n")
		for j := 0; j < 6; j++ {
			fmt.Fprintf(&b, "-removed line %d %s\n", j, strings.Repeat("old ", 15))
			fmt.Fprintf(&b, "+added line %d %s\n", j, strings.Repeat("new ", 15))
		}
		b.WriteString(" }\n")
	}
	return b.String()
}

// mirrorEmbedForSavings copies the embedded FS to disk so script
// processors have a chdir target. Reuses copyEmbedTree from the
// same package's builtins_test.go so we don't duplicate the walk.
func mirrorEmbedForSavings(dest string) error {
	return copyEmbedTree(builtins.FS, ".", dest)
}
