package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/3rg0n/thlibo/internal/ipc"
)

// daemonFixture bundles a built stub binary and a factory for Config
// tailored to per-test temp dirs and TCP-loopback IPC addresses.
type daemonFixture struct {
	stubPath string
}

func newFixture(t *testing.T) *daemonFixture {
	return &daemonFixture{stubPath: buildStub(t)}
}

// makeConfig produces a Config using TCP loopback on :0 for both
// endpoints so the test can run anywhere without needing admin rights
// for named pipes or socket directories.
func (f *daemonFixture) makeConfig(t *testing.T, engineArgs ...string) Config {
	t.Helper()
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "thlibod.lock")

	return Config{
		LockPath: lockPath,
		EngineCmd: func() *exec.Cmd {
			return exec.Command(f.stubPath, engineArgs...)
		},
		InferenceEndpoint: ipc.EndpointConfig{
			Kind: ipc.EndpointInference, Address: "127.0.0.1:0", UseTCP: true,
		},
		AdminEndpoint: ipc.EndpointConfig{
			Kind: ipc.EndpointAdmin, Address: "127.0.0.1:0", UseTCP: true,
		},
		ReadyPollInterval: 20 * time.Millisecond,
		ReadyPollTimeout:  5 * time.Second,
		StopTimeout:       2 * time.Second,
	}
}

// A4: the inference endpoint must NOT be dialable during the engine's
// loading phase; it only becomes reachable once the engine is ready.
func TestSocketNotCreatedUntilReady(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t, "-load-delay=300ms")

	// Spawn Start in a goroutine and probe the TCP listener before it
	// returns. We use the lock file path to find out which port was
	// chosen... actually we can't, because the listener isn't bound yet.
	// So instead we rely on the invariant: Start MUST NOT return until
	// after sockets are open. We observe that the goroutine hasn't
	// returned during the load delay and the Ready channel is still
	// closed only after Start returns.
	done := make(chan struct{})
	var d *Daemon
	var startErr error
	var startAt, returnedAt time.Time
	startAt = time.Now()
	go func() {
		d, startErr = Start(context.Background(), cfg)
		returnedAt = time.Now()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start never returned")
	}
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() { _ = d.Stop(stopCtx(t, 2*time.Second)) })

	// Start must not return before the load-delay elapsed.
	if returnedAt.Sub(startAt) < 250*time.Millisecond {
		t.Errorf("Start returned too early (%s); sockets may have been opened before engine was ready",
			returnedAt.Sub(startAt))
	}

	// Now sockets are open; a dial must succeed immediately.
	c, err := net.Dial("tcp", d.InferenceAddr().String())
	if err != nil {
		t.Fatalf("dial after ready: %v", err)
	}
	_ = c.Close()
}

// A10: admin clients that connect after startup see a "ready" status
// frame immediately. If we connect during loading, we'd expect
// "loading_model", but because Start blocks until ready in our API,
// the first reachable state for an admin client is "ready".
func TestAdminStatusFrameOnConnect(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t)
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(stopCtx(t, 2*time.Second)) })

	conn, err := net.Dial("tcp", d.AdminAddr().String())
	if err != nil {
		t.Fatalf("dial admin: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read admin frame: %v", err)
	}

	var resp ipc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode admin frame: %v", err)
	}
	if resp.ID != ipc.AdminID {
		t.Errorf("frame id = %q, want %q", resp.ID, ipc.AdminID)
	}
	if resp.Type != ipc.ResponseStatus {
		t.Errorf("frame type = %q, want %q", resp.Type, ipc.ResponseStatus)
	}
	if resp.Status != "ready" {
		t.Errorf("status = %q, want ready", resp.Status)
	}
}

// A12: Stop is graceful - it releases the lock (verified by
// re-acquiring it) and exits the engine child process.
func TestGracefulShutdownReleasesLock(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t)
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Lock cannot be acquired while daemon is running.
	if _, err := AcquireLock(cfg.LockPath); err != ErrAlreadyLocked {
		t.Errorf("lock was not held during run: %v", err)
	}

	if err := d.Stop(stopCtx(t, 3*time.Second)); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop returns, a new lock acquisition must succeed.
	lock, err := AcquireLock(cfg.LockPath)
	if err != nil {
		t.Fatalf("AcquireLock after Stop: %v", err)
	}
	_ = lock.Release()
}

// A12: Stop is idempotent.
func TestStopIdempotent(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t)
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := d.Stop(stopCtx(t, 2*time.Second)); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := d.Stop(stopCtx(t, 2*time.Second)); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

// A12: after Stop, the inference socket is no longer accepting.
func TestSocketsClosedAfterStop(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t)
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := d.InferenceAddr().String()
	if err := d.Stop(stopCtx(t, 2*time.Second)); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop returns, further dials should fail. Give the OS a brief
	// window to mark the port closed, then assert.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return // success: socket is no longer accepting
		}
		_ = c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("inference socket still accepting 500ms after Stop")
}

// A13 reinforcement at lifecycle level: the inference socket accepts
// only requests carrying a messages array (not admin-style frames).
// Phase 1 stubs out actual inference dispatch; this test just verifies
// the stub emits an error frame tagged with the request's id rather
// than closing silently.
func TestInferenceStubEchoesRequestID(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t)
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(stopCtx(t, 2*time.Second)) })

	conn, err := net.Dial("tcp", d.InferenceAddr().String())
	if err != nil {
		t.Fatalf("dial infer: %v", err)
	}
	defer conn.Close()

	req := `{"id":"req-test","messages":[{"role":"user","content":"hi"}]}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp ipc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "req-test" {
		t.Errorf("echoed id = %q, want req-test", resp.ID)
	}
	if resp.Type != ipc.ResponseError {
		t.Errorf("response type = %q, want error (Phase 1 stub)", resp.Type)
	}
}

// stopCtx builds a bounded context for Daemon.Stop. Uses t.Cleanup to
// cancel so we never leak goroutines from WithTimeout.
func stopCtx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
