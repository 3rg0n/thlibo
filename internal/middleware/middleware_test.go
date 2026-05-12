package middleware

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/3rg0n/thlibo/internal/processors"
	"github.com/3rg0n/thlibo/internal/router"
)

// fakeRouter lets tests drive decisions without a daemon.
type fakeRouter struct {
	decision router.Decision
	err      error
	called   int
}

func (f *fakeRouter) Ask(ctx context.Context, reg *processors.Registry, input string) (router.Decision, error) {
	f.called++
	return f.decision, f.err
}

// withScriptProcessor writes a script processor under a temp dir with
// a platform-appropriate entry that echoes stdin with a prefix. Returns
// a registry containing just that processor.
func withScriptProcessor(t *testing.T, name, matchRegex string) *processors.Registry {
	t.Helper()
	dir := t.TempDir()

	var entry, script string
	if runtime.GOOS == "windows" {
		entry = "run.py"
		script = "import sys\ndata = sys.stdin.read()\nsys.stdout.write(\"FILTERED:\" + data)\n"
	} else {
		entry = "run.sh"
		script = "#!/usr/bin/env bash\necho -n \"FILTERED:\"; cat\n"
	}

	procDir := filepath.Join(dir, name)
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(procDir, "processor.yaml")
	y := "name: " + name + "\ntype: script\nentry: " + entry + "\n"
	if matchRegex != "" {
		y += "match: " + quoteYAML(matchRegex) + "\n"
	}
	if err := os.WriteFile(yamlPath, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(procDir, entry)
	if err := os.WriteFile(entryPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	r, warnings, _ := processors.BuildFromDisk("", dir)
	if len(warnings) > 0 {
		t.Fatalf("registry warnings: %v", warnings)
	}
	return r
}

func quoteYAML(s string) string {
	if strings.ContainsAny(s, ":#\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// B1: short input passes through untouched. Router and dispatcher
// must not be called.
func TestShortInputPassesThrough(t *testing.T) {
	reg, _, _ := processors.Build(nil, nil)
	rt := &fakeRouter{}
	p := Pipeline{
		Registry:   reg,
		Router:     rt,
		Dispatcher: &processors.Dispatcher{},
	}
	short := strings.Repeat("x", MinBytesForRouting-1)
	var out bytes.Buffer
	_ = p.Process(context.Background(), strings.NewReader(short), &out)
	if out.String() != short {
		t.Errorf("output differs from input")
	}
	if rt.called != 0 {
		t.Errorf("router called %d times, want 0", rt.called)
	}
}

// B4: fast-path regex hit dispatches immediately (no router call).
func TestFastPathHitSkipsRouter(t *testing.T) {
	reg := withScriptProcessor(t, "git-filter", "^On branch")
	rt := &fakeRouter{}
	var warnings []string
	p := Pipeline{
		Registry:   reg,
		Router:     rt,
		Dispatcher: &processors.Dispatcher{},
		OnWarning:  func(s string) { warnings = append(warnings, s) },
	}
	input := "On branch main\n" + strings.Repeat("data ", MinBytesForRouting/5)
	var out bytes.Buffer
	_ = p.Process(context.Background(), strings.NewReader(input), &out)
	if !strings.HasPrefix(out.String(), "FILTERED:") {
		t.Errorf("output = %q (first 80 chars)\nwarnings: %v", out.String()[:min(80, len(out.String()))], warnings)
	}
	if rt.called != 0 {
		t.Errorf("router called %d times, want 0 (fast-path should win)", rt.called)
	}
}

// B5/B6: no fast-path -> router call. "none" (empty chain) produces
// passthrough.
func TestRouterPassthroughReturnsOriginal(t *testing.T) {
	reg := withScriptProcessor(t, "git-filter", "^WILLNOTMATCH")
	rt := &fakeRouter{decision: router.Decision{}} // empty chain
	p := Pipeline{
		Registry:   reg,
		Router:     rt,
		Dispatcher: &processors.Dispatcher{},
	}
	input := strings.Repeat("data ", MinBytesForRouting/5+10)
	var out bytes.Buffer
	_ = p.Process(context.Background(), strings.NewReader(input), &out)
	if rt.called != 1 {
		t.Errorf("router calls = %d, want 1", rt.called)
	}
	if out.String() != input {
		t.Error("passthrough should return original bytes")
	}
}

// B7: chain dispatches in order. Two script processors; second wraps
// first's output.
func TestRouterChainDispatches(t *testing.T) {
	dir := t.TempDir()
	// Two script processors: a.py prefixes A:, b.py prefixes B:.
	// Chain [a,b] yields "B:A:<input>".
	mk := func(name, prefix string) {
		d := filepath.Join(dir, name)
		_ = os.MkdirAll(d, 0o755)
		var entry, script string
		if runtime.GOOS == "windows" {
			entry = "run.py"
			script = "import sys\ndata = sys.stdin.read()\nsys.stdout.write(\"" + prefix + "\" + data)\n"
		} else {
			entry = "run.sh"
			script = "#!/usr/bin/env bash\nprintf %s \"" + prefix + "\"; cat\n"
		}
		_ = os.WriteFile(filepath.Join(d, "processor.yaml"),
			[]byte("name: "+name+"\ntype: script\nentry: "+entry+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(d, entry), []byte(script), 0o755)
	}
	mk("a", "A:")
	mk("b", "B:")
	reg, _, _ := processors.BuildFromDisk("", dir)
	rt := &fakeRouter{decision: router.Decision{Chain: []string{"a", "b"}}}
	p := Pipeline{
		Registry:   reg,
		Router:     rt,
		Dispatcher: &processors.Dispatcher{},
	}
	input := strings.Repeat("x", MinBytesForRouting+10)
	var out bytes.Buffer
	_ = p.Process(context.Background(), strings.NewReader(input), &out)
	got := out.String()
	if !strings.HasPrefix(got, "B:A:") {
		t.Errorf("chain ordering wrong; output prefix = %q", got[:min(20, len(got))])
	}
}

// B8a / B8b: router error -> passthrough.
func TestRouterErrorFallsBack(t *testing.T) {
	reg := withScriptProcessor(t, "git-filter", "^WILLNOTMATCH")
	rt := &fakeRouter{err: errors.New("daemon unreachable")}

	var warnings []string
	p := Pipeline{
		Registry:   reg,
		Router:     rt,
		Dispatcher: &processors.Dispatcher{},
		OnWarning:  func(s string) { warnings = append(warnings, s) },
	}
	input := strings.Repeat("x", MinBytesForRouting+10)
	var out bytes.Buffer
	_ = p.Process(context.Background(), strings.NewReader(input), &out)

	if out.String() != input {
		t.Error("should fall back to original input on router error")
	}
	if len(warnings) == 0 {
		t.Error("expected at least one OnWarning call")
	}
}

// B8h: empty registry -> passthrough (defensive; built-ins should
// prevent this in practice).
func TestEmptyRegistryPassesThrough(t *testing.T) {
	reg, _, _ := processors.Build(nil, nil)
	rt := &fakeRouter{}
	p := Pipeline{Registry: reg, Router: rt, Dispatcher: &processors.Dispatcher{}}
	input := strings.Repeat("x", MinBytesForRouting+10)
	var out bytes.Buffer
	_ = p.Process(context.Background(), strings.NewReader(input), &out)
	if out.String() != input {
		t.Error("passthrough expected")
	}
	if rt.called != 0 {
		t.Error("router must not be called with empty registry")
	}
}

// Ensure ScriptTimeout is honored (B8e groundwork).
func TestDispatcherScriptTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep script not portable; covered by Linux CI")
	}
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sleeper"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "sleeper", "processor.yaml"),
		[]byte("name: sleeper\ntype: script\nentry: run.sh\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sleeper", "run.sh"),
		[]byte("#!/usr/bin/env bash\nsleep 5\ncat\n"), 0o755)
	reg, _, _ := processors.BuildFromDisk("", dir)

	disp := &processors.Dispatcher{ScriptTimeout: 200 * time.Millisecond}
	_, err := disp.Run(context.Background(), reg.Get("sleeper"), "data")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want timeout", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
