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

// A backend that ignores response_format can name anything. A processor
// marked `routable: false` must be rejected there too, not just omitted
// from the enum — shorthand rewrites prose in place, so letting the model
// select it for tool output would corrupt the output, not merely waste a
// call.
func TestParseRejectsUnroutableName(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("shorthand/processor.md", "---\nname: shorthand\ntype: prompt\nroutable: false\n---\nbody\n")
	write("compress/processor.md", "---\nname: compress\ntype: prompt\n---\nbody\n")
	write("git-filter/processor.yaml", "name: git-filter\ntype: script\nentry: r.py\nmatch: \"^On branch\"\n")
	write("git-filter/r.py", "")
	reg, _, _ := processors.Build(nil, os.DirFS(dir))

	for _, name := range []string{"shorthand", "git-filter"} {
		got := parseRouteResult(routeText(`["`+name+`"]`), reg)
		if !got.Decision.Passthrough() {
			t.Errorf("%q is not router-eligible but was accepted: chain=%v", name, got.Decision.Chain)
		}
		if len(got.Unknown) == 0 {
			t.Errorf("%q should be reported as Unknown so the caller can log it", name)
		}
	}
	// The eligible one still works.
	if got := parseRouteResult(routeText(`["compress"]`), reg); got.Decision.Passthrough() {
		t.Error("eligible processor was rejected")
	}
}

// Ask with no processors short-circuits to passthrough without a
// daemon call. Confirms Ask is safe to call on a cold middleware.
// The nil Client is the assertion: dereferencing it would panic, so
// reaching this test's end proves no round-trip was attempted.
func TestAskEmptyRegistry(t *testing.T) {
	reg, _, _ := processors.Build(nil, nil)
	a := &ClientAdapter{Client: nil}
	d, err := a.Ask(context.Background(), reg, "anything")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !d.Passthrough() {
		t.Error("empty registry should produce passthrough")
	}
}

// The tokenomics case: a registry whose every processor is already
// covered by a fast-path regex has no candidate the model could usefully
// name, so Ask must not spend a round-trip. Nil Client again does the
// asserting — a call would panic.
func TestAskSkipsCallWhenNothingRoutable(t *testing.T) {
	reg := fastPathOnlyRegistry(t)
	if got := reg.RoutableNames(); len(got) != 0 {
		t.Fatalf("fixture should have no routable processors, got %v", got)
	}
	a := &ClientAdapter{Client: nil}
	d, err := a.Ask(context.Background(), reg, "On branch main\nnothing to commit\n")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !d.Passthrough() {
		t.Errorf("want passthrough, got chain %v", d.Chain)
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

// fastPathOnlyRegistry mirrors the shipped built-in set's dominant shape:
// every processor carries a match regex, so MatchFastPath answers first
// and the router has nothing left to decide.
func fastPathOnlyRegistry(t *testing.T) *processors.Registry {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("git-filter/processor.yaml", "name: git-filter\ntype: script\nentry: run.py\nmatch: \"^On branch\"\ndescription: git\n")
	write("git-filter/run.py", "")
	write("npm-filter/processor.yaml", "name: npm-filter\ntype: script\nentry: run.py\nmatch: \"npm (WARN|ERR)\"\ndescription: npm\n")
	write("npm-filter/run.py", "")
	r, _, _ := processors.Build(nil, os.DirFS(dir))
	return r
}

// The prompt must advertise only router-eligible processors, described by
// route_hint rather than the long human description. Both halves are
// tokenomics: a candidate the router can't usefully pick, and prose the
// router can't act on, are the same waste.
func TestRoutingPromptTrimsToEligibleAndUsesHints(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Fast-path covered -> excluded.
	write("git-filter/processor.yaml", "name: git-filter\ntype: script\nentry: run.py\nmatch: \"^On branch\"\ndescription: \"long human prose about git\"\n")
	write("git-filter/run.py", "")
	// Explicitly opted out -> excluded despite no match regex.
	write("shorthand/processor.md", "---\nname: shorthand\ntype: prompt\nroutable: false\ndescription: \"rewrites prose in place\"\n---\nbody\n")
	// Eligible, and carries a hint that must win over the description.
	write("compress/processor.md", "---\nname: compress\ntype: prompt\nroute_hint: \"HINTED\"\ndescription: \"DESCRIPTIVE human paragraph\"\n---\nbody\n")
	reg, _, _ := processors.Build(nil, os.DirFS(dir))

	sys := buildRoutingMessages(reg, "in")[0].Content
	if !strings.Contains(sys, "compress: HINTED") {
		t.Errorf("route_hint should describe the processor:\n%s", sys)
	}
	if strings.Contains(sys, "DESCRIPTIVE") {
		t.Error("route_hint set, yet the human description still shipped")
	}
	for _, gone := range []string{"git-filter", "shorthand"} {
		if strings.Contains(sys, gone) {
			t.Errorf("%q is not router-eligible but appears in the prompt:\n%s", gone, sys)
		}
	}
	// The schema enum must mirror the prompt, or the grammar permits a
	// name the model was never offered.
	schema := string(routeSchema(reg))
	if !strings.Contains(schema, `"compress"`) {
		t.Errorf("eligible processor missing from schema enum: %s", schema)
	}
	for _, gone := range []string{`"git-filter"`, `"shorthand"`} {
		if strings.Contains(schema, gone) {
			t.Errorf("ineligible %s present in schema enum: %s", gone, schema)
		}
	}
}
