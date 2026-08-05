package compresscmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runWithStdin points os.Stdin at a pipe carrying body for the duration
// of the call, and returns Run's exit code plus whatever it wrote to
// stdout.
//
// Run reads os.Stdin and writes os.Stdout directly (it is a subcommand
// entry point, not an io.Writer-parameterised function), so exercising
// it at all requires swapping the real descriptors. Files rather than
// in-memory buffers because os.Stdout must be an *os.File.
func runWithStdin(t *testing.T, body []byte) (int, string) {
	t.Helper()

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	go func() {
		_, _ = inW.Write(body)
		_ = inW.Close()
	}()

	outPath := filepath.Join(t.TempDir(), "stdout")
	outF, err := os.Create(outPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}

	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outF
	code := Run(nil)
	os.Stdin, os.Stdout = origIn, origOut

	_ = outF.Close()
	_ = inR.Close()

	got, err := os.ReadFile(outPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatalf("read stdout file: %v", err)
	}
	return code, string(got)
}

// isolate points the processor registry and home dir at empty temp dirs
// so a test never reads the developer's real ~/.thlibo/processors — a
// user processor there would shadow a built-in and change what these
// tests observe.
//
// Deliberately NOT an attempt to make the daemon unreachable. There is
// no env override for the socket path (inferd.DefaultGenerationAddress
// is fixed on Windows and derived from XDG_RUNTIME_DIR/HOME elsewhere),
// so these tests assert only what holds whether or not inferd happens
// to be running on the box. Byte-exact fail-open on an unreachable
// daemon is covered where it can be injected deterministically:
// middleware.TestFallbackMatrix case B8a.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("THLIBO_PROCESSORS_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// The short-circuit: input under middleware.MinBytesForRouting (2000)
// must come back byte-identical, having consulted neither the registry
// nor the daemon (invariant #3). Daemon-independent by construction —
// that is the point of the short-circuit.
func TestCompressShortCircuitsSmallInput(t *testing.T) {
	isolate(t)
	in := []byte("tiny output, well under the routing floor\n")

	code, out := runWithStdin(t, in)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if out != string(in) {
		t.Fatalf("small input was altered:\n got: %q\nwant: %q", out, in)
	}
}

// The weaker half of invariant #2 that is testable here: whatever the
// pipeline does with routable input, `compress` exits 0 and never emits
// nothing. Output loss is the failure that actually hurts — a hook that
// swallowed tool output would destroy the document it was meant to
// shrink — and it is checkable without knowing whether a daemon
// answered.
func TestCompressNeverDropsRoutableInput(t *testing.T) {
	isolate(t)
	// Deliberately not log-shaped or diff-shaped, so no built-in
	// filter's match regex claims it and the router path is the one
	// exercised.
	in := []byte(strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit\n", 80))
	if len(in) < 2000 {
		t.Fatalf("fixture is %d bytes; must exceed the 2000-byte routing floor to exercise routing", len(in))
	}

	code, out := runWithStdin(t, in)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 — compress must never fail the caller", code)
	}
	if out == "" {
		t.Fatal("compress emitted nothing for 4KB of input; output loss is the fallback contract's core failure")
	}
}

// A deterministic native filter runs on the fast-path match before the
// router is consulted (invariant #4), so git-filter compresses with no
// inference round-trip at all. This is what distinguishes "fail open"
// from "do nothing", and it is deterministic regardless of daemon state
// precisely because the fast path never reaches the daemon.
func TestCompressRunsNativeFilterOnFastPath(t *testing.T) {
	isolate(t)

	var b bytes.Buffer
	b.WriteString("diff --git a/x.go b/x.go\n")
	b.WriteString("index 1234567..89abcde 100644\n")
	b.WriteString("--- a/x.go\n+++ b/x.go\n")
	// A valid hunk header is load-bearing: without it git-filter counts
	// no lines and reports +0 -0, which looks like a filter bug.
	b.WriteString("@@ -1,3 +1,203 @@\n")
	for i := 0; i < 200; i++ {
		b.WriteString("+\tadded line of diff body content here\n")
	}
	b.WriteString("-\tremoved line\n")
	in := b.Bytes()

	code, out := runWithStdin(t, in)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(out) >= len(in) {
		t.Fatalf("git-filter did not compress: in %d bytes, out %d bytes\n%s", len(in), len(out), out)
	}
	if !strings.Contains(out, "+200") || !strings.Contains(out, "-1") {
		t.Fatalf("git-filter output lost the hunk counts:\n%s", out)
	}
}

// Empty stdin is a real case: a hook fires on a command that produced
// no output. Must exit 0 and emit nothing, not error.
func TestCompressEmptyInput(t *testing.T) {
	isolate(t)

	code, out := runWithStdin(t, nil)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if out != "" {
		t.Fatalf("empty input produced output: %q", out)
	}
}

// BuildPipeline is exported for reuse by casecmd, so its contract is
// worth pinning: every collaborator wired, and constructible without a
// reachable daemon (it only records the address; nothing dials until
// Process runs).
func TestBuildPipelineWiresCollaborators(t *testing.T) {
	isolate(t)

	p, err := BuildPipeline()
	if err != nil {
		t.Fatalf("BuildPipeline: %v", err)
	}
	if p.Registry == nil {
		t.Error("Registry is nil")
	}
	if p.Router == nil {
		t.Error("Router is nil")
	}
	if p.Dispatcher == nil {
		t.Error("Dispatcher is nil")
	}
	// Telemetry must be non-nil even when disabled: decide()
	// unconditionally hands it an Invocation, so a nil here is a panic
	// on the hot path rather than a missing metric.
	if p.Telemetry == nil {
		t.Error("Telemetry is nil; Init must return a no-op rather than nil")
	}
}

// The built-in filters must be present even when the user processors
// dir is empty — they are embedded via go:embed, not read from disk.
// Guards against a BuildRegistry change that made the user source
// mandatory.
func TestBuildPipelineHasBuiltinsWithEmptyUserDir(t *testing.T) {
	isolate(t)

	p, err := BuildPipeline()
	if err != nil {
		t.Fatalf("BuildPipeline: %v", err)
	}
	for _, name := range []string{"git-filter", "pdf-filter", "cordon-filter", "compress"} {
		if p.Registry.Get(name) == nil {
			t.Errorf("built-in %q missing from registry built over an empty user dir", name)
		}
	}
}

// THLIBO_PROCESSORS_DIR pointing at a path that does not exist must not
// be an error: BuildRegistry skips a missing user source rather than
// failing, so a fresh install with no ~/.thlibo/processors still gets a
// working pipeline with the built-ins.
func TestBuildPipelineToleratesMissingUserDir(t *testing.T) {
	isolate(t)
	t.Setenv("THLIBO_PROCESSORS_DIR", filepath.Join(t.TempDir(), "does", "not", "exist"))

	p, err := BuildPipeline()
	if err != nil {
		t.Fatalf("BuildPipeline with absent user dir: %v", err)
	}
	if p.Registry.Get("git-filter") == nil {
		t.Error("built-ins missing when the user dir does not exist")
	}
}
