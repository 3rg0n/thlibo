package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/processors"
)

// buildRegistry stitches together a small registry for router tests.
func buildRegistry(t *testing.T) *processors.Registry {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("git-filter/processor.yaml", "name: git-filter\ntype: script\nentry: run.py\ndescription: \"git status/diff/log\"\n")
	write("git-filter/run.py", "")
	write("casefolder/processor.md", "---\nname: casefolder\ntype: prompt\ndescription: \"stack traces\"\n---\nbody\n")
	write("compress/processor.md", "---\nname: compress\ntype: prompt\ndescription: \"general\"\n---\nbody\n")
	r, _, _ := processors.Build(nil, os.DirFS(dir))
	return r
}

// B5: routing prompt lists every registered processor in deterministic
// order, includes the (truncated) input, and never references anything
// outside the registry.
func TestBuildRoutingMessages(t *testing.T) {
	reg := buildRegistry(t)
	msgs := buildRoutingMessages(reg, "some input here")
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (system+user)", len(msgs))
	}
	sys := msgs[0].Content
	for _, want := range []string{"casefolder", "compress", "git-filter"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
	// Ordering: alphabetical. casefolder before compress before git-filter.
	if strings.Index(sys, "casefolder") > strings.Index(sys, "compress") {
		t.Error("processors not listed alphabetically")
	}
	if msgs[1].Content != "some input here" {
		t.Errorf("user = %q", msgs[1].Content)
	}
}

// B5: grammar enumerates every processor name. An empty registry
// produces a grammar forcing empty chain (passthrough).
func TestBuildGrammar(t *testing.T) {
	reg := buildRegistry(t)
	g := buildGrammar(reg.Names())
	for _, want := range []string{`"casefolder"`, `"compress"`, `"git-filter"`, "chain"} {
		if !strings.Contains(g, want) {
			t.Errorf("grammar missing %q in %q", want, g)
		}
	}

	empty := buildGrammar(nil)
	// Grammar is a GBNF string literal, so the JSON-braced chain
	// appears as an embedded escaped form.
	if !strings.Contains(empty, `\"chain\":[]`) {
		t.Errorf("empty grammar = %q", empty)
	}
}

// B5/B6: happy paths and the passthrough decision.
func TestParseRoutingResponse(t *testing.T) {
	reg := buildRegistry(t)

	cases := []struct {
		name     string
		raw      string
		wantPass bool
		wantChain []string
	}{
		{"single", `{"chain":["git-filter"]}`, false, []string{"git-filter"}},
		{"chain", `{"chain":["git-filter","compress"]}`, false, []string{"git-filter", "compress"}},
		{"empty chain = passthrough", `{"chain":[]}`, true, nil},
		{"fenced code block", "```json\n{\"chain\":[\"git-filter\"]}\n```", false, []string{"git-filter"}},
		{"with preamble", `sure: {"chain":["compress"]}`, false, []string{"compress"}},
		{"B8c malformed", `not json`, true, nil},
		{"B8c unknown name", `{"chain":["nonexistent"]}`, true, nil},
		{"B8c partial unknown", `{"chain":["git-filter","nonexistent"]}`, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseRoutingResponse(c.raw, reg)
			if got.Passthrough() != c.wantPass {
				t.Errorf("Passthrough = %v, want %v (chain=%v)", got.Passthrough(), c.wantPass, got.Chain)
			}
			if !c.wantPass && !equalSlice(got.Chain, c.wantChain) {
				t.Errorf("chain = %v, want %v", got.Chain, c.wantChain)
			}
		})
	}
}

// Ask with no processors short-circuits to passthrough without a
// daemon call. Confirms Ask is safe to call on a cold middleware.
func TestAskEmptyRegistry(t *testing.T) {
	reg, _, _ := processors.Build(nil, nil)
	d, err := Ask(context.Background(), nil, reg, "anything")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !d.Passthrough() {
		t.Error("empty registry should produce passthrough")
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
