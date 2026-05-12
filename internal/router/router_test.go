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

// B5: routing messages include a Gemma tool declaration and list every
// registered processor in deterministic order.
func TestBuildRoutingMessages(t *testing.T) {
	reg := buildRegistry(t)
	msgs := buildRoutingMessages(reg, "some input here")
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (system+user)", len(msgs))
	}
	sys := msgs[0].Content
	if !strings.Contains(sys, `<|tool>declaration:route`) {
		t.Errorf("system prompt missing Gemma tool declaration: %q", sys[:minInt(200, len(sys))])
	}
	if !strings.Contains(sys, `<tool|>`) {
		t.Error("system prompt missing tool-declaration close token")
	}
	for _, want := range []string{"casefolder", "compress", "git-filter"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing processor name %q", want)
		}
	}
	// Ordering: alphabetical.
	if strings.Index(sys, "casefolder") > strings.Index(sys, "compress") {
		t.Error("processors not listed alphabetically")
	}
	if msgs[1].Content != "some input here" {
		t.Errorf("user = %q", msgs[1].Content)
	}
}

// B5: grammar targets Gemma's tool-call output format and enumerates
// every processor name. Empty registry produces a grammar with no
// name alternatives (empty chain only).
func TestBuildGrammar(t *testing.T) {
	reg := buildRegistry(t)
	g := buildGrammar(reg.Names())
	for _, want := range []string{
		`<|tool_call>call:route`,
		`processors:[`,
		`<tool_call|>`,
		`"casefolder"`,
		`"compress"`,
		`"git-filter"`,
	} {
		if !strings.Contains(g, want) {
			t.Errorf("grammar missing %q in:\n%s", want, g)
		}
	}

	empty := buildGrammar(nil)
	if !strings.Contains(empty, `<|tool_call>call:route{processors:[`) {
		t.Errorf("empty-registry grammar missing tool-call scaffolding: %q", empty)
	}
	// Empty grammar must not declare any name alternatives.
	if strings.Contains(empty, `name ::=`) {
		t.Errorf("empty grammar unexpectedly has a name rule: %q", empty)
	}
}

// B5/B6: happy paths and the passthrough decision against Gemma's
// native tool-call output.
func TestParseRoutingResponse(t *testing.T) {
	reg := buildRegistry(t)

	single := `<|tool_call>call:route{processors:[<|"|>git-filter<|"|>]}<tool_call|>`
	chain := `<|tool_call>call:route{processors:[<|"|>git-filter<|"|>,<|"|>compress<|"|>]}<tool_call|>`
	empty := `<|tool_call>call:route{processors:[]}<tool_call|>`

	cases := []struct {
		name      string
		raw       string
		wantPass  bool
		wantChain []string
	}{
		{"single processor", single, false, []string{"git-filter"}},
		{"chain", chain, false, []string{"git-filter", "compress"}},
		{"empty = passthrough", empty, true, nil},
		{"B8c garbage", `just some text`, true, nil},
		{"B8c no tool call", `{"chain":["git-filter"]}`, true, nil},
		{"B8c unknown name", `<|tool_call>call:route{processors:[<|"|>nonexistent<|"|>]}<tool_call|>`, true, nil},
		{"B8c partial unknown = drop", `<|tool_call>call:route{processors:[<|"|>git-filter<|"|>,<|"|>nonexistent<|"|>]}<tool_call|>`, true, nil},
		{"leading whitespace tolerated", "\n\n" + single, false, []string{"git-filter"}},
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
