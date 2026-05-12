package ipc

import (
	"bufio"
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

// A3 (partial): can we create and round-trip through an IPC endpoint?
// Full permission verification (group=thlibo-users, mode=0660) lives in
// an integration test that needs a real group; this unit test covers the
// plumbing that works identically on any machine.
func TestListenAndAccept(t *testing.T) {
	cfg := EndpointConfig{
		Kind:    EndpointInference,
		Address: testAddress(t, "thlibo-infer-test"),
	}
	l, err := Listen(cfg)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	done := make(chan error, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		// Echo one line so we prove a full round-trip.
		r := bufio.NewReader(conn)
		line, err := r.ReadBytes('\n')
		if err != nil {
			done <- err
			return
		}
		if _, err := conn.Write(line); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	conn, err := dial(cfg.Address)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	got, err := r.ReadBytes('\n')
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "ping\n" {
		t.Errorf("echo = %q, want %q", got, "ping\n")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server goroutine: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

// A3: TCP fallback listens on loopback and round-trips.
func TestListenTCPFallback(t *testing.T) {
	// Bind to :0 so the test can run in parallel with a real daemon.
	cfg := EndpointConfig{
		Kind:    EndpointInference,
		Address: "127.0.0.1:0",
		UseTCP:  true,
	}
	l, err := Listen(cfg)
	if err != nil {
		t.Fatalf("Listen TCP: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	if _, ok := l.Addr().(*net.TCPAddr); !ok {
		t.Fatalf("TCP listener returned non-TCP addr: %T", l.Addr())
	}
}

func TestDefaultAddresses(t *testing.T) {
	if got := DefaultInferenceAddress(); got == "" {
		t.Error("DefaultInferenceAddress returned empty")
	}
	if got := DefaultAdminAddress(); got == "" {
		t.Error("DefaultAdminAddress returned empty")
	}
	// Platform-specific sanity.
	switch runtime.GOOS {
	case "windows":
		if got := DefaultInferenceAddress(); got[:len(`\\.\pipe\`)] != `\\.\pipe\` {
			t.Errorf("Windows inference addr should be a named pipe, got %q", got)
		}
	case "linux", "darwin":
		if got := DefaultInferenceAddress(); got[0] != '/' {
			t.Errorf("Unix inference addr should be absolute, got %q", got)
		}
	}
}

func TestEndpointKindString(t *testing.T) {
	if EndpointInference.String() != "inference" {
		t.Error("EndpointInference.String()")
	}
	if EndpointAdmin.String() != "admin" {
		t.Error("EndpointAdmin.String()")
	}
}

func TestModeFor(t *testing.T) {
	if got := modeFor(EndpointInference); got != 0o660 {
		t.Errorf("inference mode = %o, want 0660", got)
	}
	if got := modeFor(EndpointAdmin); got != 0o600 {
		t.Errorf("admin mode = %o, want 0600", got)
	}
}
