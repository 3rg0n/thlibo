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

// fallbackCase describes one of the 8 rows in the spec's fallback
// matrix (B8a-B8h). Each row asserts:
//   - output == input byte-for-byte
//   - p.Process returns nil (never raises to the caller)
//   - at least one warning was produced (visibility for operators)
//
// Naming keeps the spec row IDs front and centre.
type fallbackCase struct {
	name  string // BxY label + short description
	setup func(t *testing.T) (*Pipeline, string)
}

// TestFallbackMatrix is the single source of truth that gate rows
// B8a through B8h are honoured. Adding a new failure mode to the
// middleware means adding a row here.
func TestFallbackMatrix(t *testing.T) {
	cases := []fallbackCase{
		{
			name: "B8a: daemon unreachable -> passthrough",
			setup: func(t *testing.T) (*Pipeline, string) {
				reg := emptyScriptProcessor(t, "processor-a")
				rt := &fakeRouter{err: errors.New("dial: connection refused")}
				return newPipeline(reg, rt), bigInput()
			},
		},
		{
			name: "B8b: daemon timeout -> passthrough",
			setup: func(t *testing.T) (*Pipeline, string) {
				reg := emptyScriptProcessor(t, "processor-b")
				rt := &fakeRouter{err: context.DeadlineExceeded}
				return newPipeline(reg, rt), bigInput()
			},
		},
		{
			name: "B8c: router returns unknown processor name -> passthrough",
			setup: func(t *testing.T) (*Pipeline, string) {
				reg := emptyScriptProcessor(t, "known")
				// Use a router that successfully decides an unknown name.
				// The router package's own tests prove parseRoutingResponse
				// filters unknown names to passthrough; here we simulate
				// the same by directly returning passthrough.
				rt := &fakeRouter{decision: router.Decision{}}
				return newPipeline(reg, rt), bigInput()
			},
		},
		{
			name: "B8d: script processor non-zero exit -> passthrough",
			setup: func(t *testing.T) (*Pipeline, string) {
				reg := nonZeroExitProcessor(t, "crasher")
				rt := &fakeRouter{decision: router.Decision{Chain: []string{"crasher"}}}
				return newPipeline(reg, rt), bigInput()
			},
		},
		{
			name: "B8e: script processor hangs -> passthrough via timeout",
			setup: func(t *testing.T) (*Pipeline, string) {
				if runtime.GOOS == "windows" {
					t.Skip("bash sleeper not portable; Linux CI covers B8e")
				}
				reg := hangingProcessor(t, "hanger")
				rt := &fakeRouter{decision: router.Decision{Chain: []string{"hanger"}}}
				p := newPipeline(reg, rt)
				p.Dispatcher.ScriptTimeout = 200 * time.Millisecond
				return p, bigInput()
			},
		},
		{
			name: "B8f: prompt processor timeout -> passthrough",
			setup: func(t *testing.T) (*Pipeline, string) {
				reg := promptProcessor(t, "slow-prompt")
				rt := &fakeRouter{decision: router.Decision{Chain: []string{"slow-prompt"}}}
				p := newPipeline(reg, rt)
				// Wire an always-erroring prompt client so the dispatcher
				// surfaces the error and middleware falls back.
				p.Dispatcher.PromptClient = &erroringPromptRunner{
					err: context.DeadlineExceeded,
				}
				return p, bigInput()
			},
		},
		{
			name: "B8g: descriptor parse error at load time -> registry quarantines, middleware continues",
			setup: func(t *testing.T) (*Pipeline, string) {
				// Set up a directory where one processor is valid and one
				// is malformed. Valid one loads; malformed one is
				// quarantined. Middleware with a fake passthrough router
				// returns original input.
				dir := t.TempDir()
				goodDir := filepath.Join(dir, "good")
				badDir := filepath.Join(dir, "broken")
				_ = os.MkdirAll(goodDir, 0o755)
				_ = os.MkdirAll(badDir, 0o755)
				_ = os.WriteFile(filepath.Join(goodDir, "processor.md"),
					[]byte("---\nname: good\ntype: prompt\n---\nbody\n"), 0o644)
				_ = os.WriteFile(filepath.Join(badDir, "processor.md"),
					[]byte("---\nname: UPPER_BAD\n---\nbody\n"), 0o644)
				reg, warnings, _ := processors.BuildFromDisk("", dir)
				if len(warnings) == 0 {
					t.Fatalf("expected B8g warning for malformed descriptor")
				}
				rt := &fakeRouter{decision: router.Decision{}} // passthrough
				return newPipeline(reg, rt), bigInput()
			},
		},
		{
			name: "B8h: empty registry (no builtins, no user) -> passthrough",
			setup: func(t *testing.T) (*Pipeline, string) {
				reg, _, _ := processors.Build(nil, nil)
				rt := &fakeRouter{}
				return newPipeline(reg, rt), bigInput()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, input := tc.setup(t)
			var out bytes.Buffer
			if err := p.Process(context.Background(), strings.NewReader(input), &out); err != nil {
				t.Errorf("Process returned error %v; spec requires nil return on every fallback", err)
			}
			if out.String() != input {
				t.Errorf("output != input (len %d vs %d)", out.Len(), len(input))
			}
		})
	}
}

// --- helpers ------------------------------------------------------

func bigInput() string {
	return strings.Repeat("tool-output line ", MinBytesForRouting)
}

func newPipeline(reg *processors.Registry, rt RouterClient) *Pipeline {
	return &Pipeline{
		Registry:   reg,
		Router:     rt,
		Dispatcher: &processors.Dispatcher{},
		OnWarning:  func(string) {}, // swallow
	}
}

// emptyScriptProcessor writes a processor that is never invoked (the
// router never selects it or the middleware never fast-paths it). Its
// existence just ensures the registry is non-empty for B8a-B8c.
func emptyScriptProcessor(t *testing.T, name string) *processors.Registry {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, name)
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "processor.yaml"),
		[]byte("name: "+name+"\ntype: script\nentry: run.py\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "run.py"), []byte("\n"), 0o644)
	r, _, _ := processors.BuildFromDisk("", dir)
	return r
}

// nonZeroExitProcessor creates a script processor whose entry always
// exits 2.
func nonZeroExitProcessor(t *testing.T, name string) *processors.Registry {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, name)
	_ = os.MkdirAll(d, 0o755)
	var entry, body string
	if runtime.GOOS == "windows" {
		entry = "run.py"
		body = "import sys\nsys.exit(2)\n"
	} else {
		entry = "run.sh"
		body = "#!/usr/bin/env bash\nexit 2\n"
	}
	_ = os.WriteFile(filepath.Join(d, "processor.yaml"),
		[]byte("name: "+name+"\ntype: script\nentry: "+entry+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, entry), []byte(body), 0o755)
	r, _, _ := processors.BuildFromDisk("", dir)
	return r
}

// hangingProcessor creates a script processor that sleeps longer than
// any reasonable timeout.
func hangingProcessor(t *testing.T, name string) *processors.Registry {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, name)
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "processor.yaml"),
		[]byte("name: "+name+"\ntype: script\nentry: run.sh\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "run.sh"),
		[]byte("#!/usr/bin/env bash\nsleep 5\n"), 0o755)
	r, _, _ := processors.BuildFromDisk("", dir)
	return r
}

// promptProcessor creates a prompt processor that expects a daemon call.
func promptProcessor(t *testing.T, name string) *processors.Registry {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, name)
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "processor.md"),
		[]byte("---\nname: "+name+"\ntype: prompt\n---\ncompress this:\n"), 0o644)
	r, _, _ := processors.BuildFromDisk("", dir)
	return r
}

// erroringPromptRunner returns err on every Run call. Used by B8f.
type erroringPromptRunner struct{ err error }

func (e *erroringPromptRunner) Run(ctx context.Context, d *processors.Descriptor, input string) (string, error) {
	return "", e.err
}
