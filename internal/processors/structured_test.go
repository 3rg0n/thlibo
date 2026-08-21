package processors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtins "github.com/3rg0n/thlibo/processors"
)

// TestStructuredDocumentRecognisesConfigShapes covers the formats named in
// #129 plus the client configs that motivated it.
func TestStructuredDocumentRecognisesConfigShapes(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"json object", `{"hooks": {"preToolUse": [{"command": "x"}]}, "version": 1}`},
		{"json array", `[{"a": 1}, {"b": 2}]`},
		{"json indented", "{\n  \"version\": 1,\n  \"hooks\": {\n    \"preToolUse\": []\n  }\n}"},
		{"toml tables", "[features]\nhooks = true\n\n[[hooks.PostToolUse]]\ncommand = \"thlibo\"\n"},
		{"toml pairs only", "model = \"opus\"\napproval_policy = \"on-request\"\n"},
		{"toml comments", "# thlibo\n[features]\nhooks = true\n"},
		{"toml multiline array", "deny = [\n  \"rm\",\n  \"sudo\",\n]\n[policy]\nversion = 1\n"},
		{"toml triple quoted", "banner = \"\"\"\nline one\nline two\n\"\"\"\n[meta]\nv = 1\n"},
		{"toml inline table", "target = { arch = \"arm64\", os = \"windows\" }\nname = \"thlibo\"\n"},
		{"yaml mapping with nesting", "version: 2\nupdates:\n  - package-ecosystem: gomod\n    directory: /\n"},
		{"yaml nested mapping", "name: CI\njobs:\n  test:\n    runs-on: ubuntu-latest\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !StructuredDocument(tc.in) {
				t.Errorf("want structured, got false for:\n%s", tc.in)
			}
		})
	}
}

// TestStructuredDocumentRejectsToolOutput pins the shapes that must keep
// reaching the router. The YAML cases are the dangerous ones: YAML parses
// a prose line with a colon as a mapping, so `LEVEL: message` log output
// would false-positive without the nesting requirement in yamlDocument.
func TestStructuredDocumentRejectsToolOutput(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"empty", ""},
		{"whitespace", "   \n\t\n"},
		{"one prose line with colon", "Error: connection refused to 10.0.0.1"},
		{"flat level-message log", "INFO: starting up\nWARN: slow query\nERROR: timed out\n"},
		{"flat key-value log", "time: 12:00:01\nlevel: info\nmsg: served request\n"},
		{"ndjson", "{\"level\":\"info\",\"msg\":\"a\"}\n{\"level\":\"warn\",\"msg\":\"b\"}\n"},
		{"git status", "On branch main\nnothing to commit, working tree clean\n"},
		{"go test output", "=== RUN   TestFoo\n--- PASS: TestFoo (0.01s)\nok  \tpkg\t0.02s\n"},
		{"stack trace", "panic: bad\n\ngoroutine 1 [running]:\nmain.main()\n\t/x/main.go:12 +0x1d\n"},
		{"bare json scalar", `"just a string"`},
		{"json with trailing garbage", "{\"a\": 1}\nsome log line\n"},
		{"toml with a prose line", "[features]\nhooks = true\nthis line is prose\n"},
		{"single toml pair", "hooks = true\n"},
		{"compress output", "sig=workflow-config\nlevel=info\ncount=11\ntail=24→2\n"},
		{"env dump", "PATH=/usr/bin:/bin\nHOME=/home/x\nSHELL=/bin/bash\n"},
		{"git config list", "user.email=a@b.com\ncore.autocrlf=true\ninit.defaultbranch=main\n"},
		{"numeric metrics dump", "retries=3\ntimeout=30\nmax_conns=100\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if StructuredDocument(tc.in) {
				t.Errorf("want NOT structured, got true for:\n%s", tc.in)
			}
		})
	}
}

// TestStructuredDocumentNoFalsePositiveOnFixtureCorpus is the guard that
// matters. A false positive turns thlibo into a silent no-op for that
// input — the #106 failure class — so every real tool-output fixture in
// testdata/ must either be refused by StructuredDocument or be claimed by
// a fast-path filter, which runs first in decide() and therefore wins.
//
// har-filter's fixture is legitimately whole JSON, and that is exactly why
// the assertion is a disjunction rather than a flat "must be false".
func TestStructuredDocumentNoFalsePositiveOnFixtureCorpus(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "*", "*.input"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no .input fixtures found — corpus assertion would be vacuous")
	}

	reg, warnings, err := BuildFromSources(Source{FS: builtins.FS, Origin: OriginBuiltin})
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	for _, w := range warnings {
		t.Logf("registry warning: %v", w)
	}

	checked := 0
	for _, path := range inputs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		if strings.TrimSpace(body) == "" {
			continue
		}
		checked++
		if !StructuredDocument(body) {
			continue
		}
		if d := reg.MatchFastPath(body); d != nil {
			t.Logf("%s: whole-document JSON, claimed by %s on the fast path", path, d.Name)
			continue
		}
		t.Errorf("%s: StructuredDocument=true with no fast-path claim — this "+
			"input would stop being compressed", path)
	}
	t.Logf("checked %d non-empty fixtures", checked)
}
