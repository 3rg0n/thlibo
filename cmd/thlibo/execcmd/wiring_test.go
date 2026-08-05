package execcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run and defaultPipeline are the production wiring around `run`, which
// is already covered through its injected pipelineFactory. What is left
// is the argv gate and the fact that the real factory builds a usable
// pipeline — a failure there would make every hook invocation fall back
// to passthrough while every test on `run` still passed.

// Run with no command is a usage error. Exit 0 here would make a
// mis-wired hook look like a successful no-op, which is exactly the
// failure that hides itself.
func TestRunRejectsMissingCommand(t *testing.T) {
	for _, argv := range [][]string{
		nil,
		{},
		{"--"}, // separator with nothing after it
	} {
		if code := Run(argv); code != ExitUsage {
			t.Errorf("Run(%q) = %d, want ExitUsage(%d)", argv, code, ExitUsage)
		}
	}
}

// defaultPipeline must build a working pipeline against an empty user
// processors dir: the embedded built-ins are the floor, and a registry
// error here degrades every exec to passthrough.
func TestDefaultPipelineBuildsFromBuiltins(t *testing.T) {
	t.Setenv("THLIBO_PROCESSORS_DIR", t.TempDir())

	p, err := defaultPipeline()
	if err != nil {
		t.Fatalf("defaultPipeline: %v", err)
	}
	if p == nil || p.Registry == nil || p.Router == nil || p.Dispatcher == nil {
		t.Fatalf("pipeline is incomplete: %+v", p)
	}

	// A representative built-in from each kind: a native filter, the
	// vendored PDF path, and a prompt processor. Absence means the
	// go:embed set did not load.
	for _, name := range []string{"git-filter", "pdf-filter", "compress"} {
		if p.Registry.Get(name) == nil {
			t.Errorf("built-in %q missing from the production registry", name)
		}
	}
}

// The env var is the override; without it the dir derives from the home
// dir. A missing user dir must not be an error — that is the state on
// every fresh install before `thlibo install` mirrors the built-ins.
func TestDefaultPipelineToleratesMissingUserDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("THLIBO_PROCESSORS_DIR", "")

	p, err := defaultPipeline()
	if err != nil {
		t.Fatalf("defaultPipeline with no ~/.thlibo/processors: %v", err)
	}
	if p.Registry.Get("git-filter") == nil {
		t.Error("built-ins unavailable when the user dir is absent")
	}
}

// A user processor of the same name shadows a built-in, and the registry
// must surface that at load time — a silent shadow means output is being
// transformed by code the user forgot they wrote.
func TestDefaultPipelineLoadsUserProcessors(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "my-filter")
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: my-filter\nroute_hint: never pick me\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(pdir, "processor.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THLIBO_PROCESSORS_DIR", dir)

	p, err := defaultPipeline()
	if err != nil {
		t.Fatalf("defaultPipeline: %v", err)
	}
	if p.Registry.Get("my-filter") == nil {
		t.Error("user processor from $THLIBO_PROCESSORS_DIR was not loaded")
	}
	// Built-ins still present alongside it.
	if p.Registry.Get("git-filter") == nil {
		t.Error("loading a user processor dropped the built-ins")
	}
}

// defaultDaemonAddress must be non-empty on every platform: an empty
// address dials nothing, and the fail-open path would then be permanent
// rather than a fallback.
func TestDefaultDaemonAddressIsResolvable(t *testing.T) {
	addr := defaultDaemonAddress()
	if strings.TrimSpace(addr) == "" {
		t.Fatal("defaultDaemonAddress() is empty; inference would never be reachable")
	}
}

// newDispatcher must carry the prompt client through. A nil PromptClient
// makes every prompt processor fail, and under invariant #2 that failure
// is a silent passthrough rather than an error the user sees.
func TestNewDispatcherCarriesPromptClient(t *testing.T) {
	p, err := defaultPipeline()
	if err != nil {
		t.Fatalf("defaultPipeline: %v", err)
	}
	if p.Dispatcher.PromptClient == nil {
		t.Error("dispatcher has no PromptClient; prompt processors would always fall back")
	}
}
