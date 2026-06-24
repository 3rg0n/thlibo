package router

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/inferd"
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

// Routing messages describe the task and list every registered
// processor in deterministic order. The output shape is constrained via
// response_format (see TestBuildRouteRequest), not embedded as a tool.
func TestBuildRoutingMessages(t *testing.T) {
	reg := buildRegistry(t)
	msgs := buildRoutingMessages(reg, "some input here")
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (system+user)", len(msgs))
	}
	sys := msgs[0].Content
	if !strings.Contains(sys, "processors") {
		t.Error("system prompt should describe the processors JSON output")
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

// The routing request constrains output via response_format (json_schema),
// not the tools mechanism — that's how v0.5 restores the hard guarantee.
func TestBuildRouteRequest(t *testing.T) {
	reg := buildRegistry(t)
	req := buildRouteRequest(reg, "in")
	if req.ResponseFormat == nil {
		t.Fatal("request must carry a response_format")
	}
	if req.ResponseFormat.Type != "json_schema" {
		t.Errorf("response_format type = %q, want json_schema", req.ResponseFormat.Type)
	}
	if len(req.Tools) != 0 {
		t.Errorf("v0.5 router should not use the tools mechanism; got %d tools", len(req.Tools))
	}
	if !strings.Contains(string(req.ResponseFormat.Schema), `"processors"`) {
		t.Errorf("schema missing processors property: %s", req.ResponseFormat.Schema)
	}
}

// routeSchema is the JSON Schema the router constrains output to. It
// enumerates the registered processor names; empty registry still
// produces valid JSON.
func TestRouteSchema(t *testing.T) {
	reg := buildRegistry(t)
	schema := string(routeSchema(reg))
	for _, want := range []string{`"processors"`, `"casefolder"`, `"compress"`, `"git-filter"`, `"required"`} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %q in:\n%s", want, schema)
		}
	}
	var js map[string]any
	if err := json.Unmarshal(routeSchema(reg), &js); err != nil {
		t.Errorf("schema is not valid JSON: %v", err)
	}
	var ejs map[string]any
	if err := json.Unmarshal(routeSchema(emptyRegistry(t)), &ejs); err != nil {
		t.Errorf("empty-registry schema invalid JSON: %v", err)
	}
}

// routeText builds a Result the way a structured-output daemon delivers
// one: the schema-constrained JSON object as response text.
func routeText(processorsJSON string) inferd.Result {
	return inferd.Result{Text: `{"processors":` + processorsJSON + `}`}
}

// B5/B6: happy paths and the passthrough decision against the model's
// schema-constrained JSON text.
func TestParseRouteResult(t *testing.T) {
	reg := buildRegistry(t)

	cases := []struct {
		name      string
		res       inferd.Result
		wantPass  bool
		wantChain []string
	}{
		{"single processor", routeText(`["git-filter"]`), false, []string{"git-filter"}},
		{"chain", routeText(`["git-filter","compress"]`), false, []string{"git-filter", "compress"}},
		{"empty = passthrough", routeText(`[]`), true, nil},
		{"surrounding whitespace tolerated", inferd.Result{Text: "\n  {\"processors\":[\"git-filter\"]}  \n"}, false, []string{"git-filter"}},
		{"B8c empty text", inferd.Result{Text: ""}, true, nil},
		{"B8c non-JSON text", inferd.Result{Text: "just some text"}, true, nil},
		{"B8c wrong shape", inferd.Result{Text: `{"chain":["git-filter"]}`}, true, nil},
		{"B8c unknown name", routeText(`["nonexistent"]`), true, nil},
		{"B8c partial unknown = drop", routeText(`["git-filter","nonexistent"]`), true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseRouteResult(c.res, reg).Decision
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

// emptyRegistry returns a registry with no processors.
func emptyRegistry(t *testing.T) *processors.Registry {
	t.Helper()
	r, _, _ := processors.Build(nil, nil)
	return r
}
