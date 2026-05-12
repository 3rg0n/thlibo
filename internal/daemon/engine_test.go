package daemon

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// buildStub compiles internal/daemon/llamafiletest into a binary in t.TempDir()
// and returns its path. Compilation happens once per test function thanks
// to sync.Once indirection from the caller. We use go build rather than
// go test -c because the stub is a standalone main, not a test binary.
func buildStub(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), stubBinaryName())
	cmd := exec.Command("go", "build", "-o", out, "./llamafiletest")
	// Run relative to the daemon package so the "./llamafiletest" path
	// resolves. Test working directory is already the package dir.
	cmd.Dir = "."
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build stub: %v\n%s", err, combined)
	}
	return out
}

func stubBinaryName() string {
	if runtime.GOOS == "windows" {
		return "llamafile-stub.exe"
	}
	return "llamafile-stub"
}

// A1: the daemon spawns a private child and can communicate with it.
// This drives the full SubprocessEngine lifecycle: start, wait for
// ready, generate, stop.
func TestEngineGenerateRoundTrip(t *testing.T) {
	stub := buildStub(t)

	cmd := exec.Command(stub, "-tokens=one,two,three")
	eng, err := StartSubprocessEngine(cmd)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = eng.Stop(1 * time.Second) })

	if !waitReady(eng, 2*time.Second) {
		t.Fatal("engine never became ready")
	}

	tokens := make(chan string, 10)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := eng.Generate(context.Background(), GeneratePrompt{
			System: "you are a test",
			User:   "hi",
		}, tokens)
		if err != nil {
			t.Errorf("Generate: %v", err)
		}
		close(tokens)
	}()

	var got []string
	for tok := range tokens {
		got = append(got, tok)
	}
	wg.Wait()

	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tokens[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A1 + spec §Daemon lifecycle step 3: Generate must fail fast if the
// engine hasn't finished loading yet.
func TestEngineRejectsGenerateWhileLoading(t *testing.T) {
	stub := buildStub(t)

	cmd := exec.Command(stub, "-load-delay=500ms")
	eng, err := StartSubprocessEngine(cmd)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = eng.Stop(2 * time.Second) })

	// While loading, Generate must return ErrNotReady.
	tokens := make(chan string, 1)
	if err := eng.Generate(context.Background(), GeneratePrompt{User: "hi"}, tokens); err != ErrNotReady {
		t.Errorf("Generate during load: err=%v, want ErrNotReady", err)
	}
}

// A1: Stop cleanly terminates the child.
func TestEngineStopCleanly(t *testing.T) {
	stub := buildStub(t)

	cmd := exec.Command(stub)
	eng, err := StartSubprocessEngine(cmd)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !waitReady(eng, 2*time.Second) {
		t.Fatal("engine never became ready")
	}

	if err := eng.Stop(2 * time.Second); err != nil {
		t.Errorf("Stop: %v", err)
	}

	select {
	case <-eng.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done channel never closed")
	}
}

// Context cancellation during Generate must return promptly. Uses the
// stub's token-delay to hold tokens back so we can cancel mid-stream.
func TestEngineCancelMidStream(t *testing.T) {
	stub := buildStub(t)

	cmd := exec.Command(stub, "-tokens=a,b,c,d,e,f,g,h,i,j", "-token-delay=100ms")
	eng, err := StartSubprocessEngine(cmd)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = eng.Stop(2 * time.Second) })

	if !waitReady(eng, 2*time.Second) {
		t.Fatal("engine never became ready")
	}

	ctx, cancel := context.WithCancel(context.Background())
	tokens := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- eng.Generate(ctx, GeneratePrompt{User: "hi"}, tokens)
	}()

	// Consume 2 tokens so we're definitely mid-stream, then cancel.
	<-tokens
	<-tokens
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Generate err after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Generate did not return after cancel")
	}
}

func waitReady(e Engine, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if e.Ready() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
