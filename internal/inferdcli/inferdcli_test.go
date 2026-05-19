package inferdcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	inferd "github.com/3rg0n/inferd/clients/go"
)

// fakeInferd is a TCP-loopback test server that mimics inferd's
// inference-socket protocol just well enough to exercise inferdcli's
// stream-collapse + error-mapping logic. Each connection reads one
// JSON request line and replies with the canned frames given to
// newFakeInferd.
type fakeInferd struct {
	t      *testing.T
	addr   string
	frames []inferd.Response
	stop   chan struct{}
}

func newFakeInferd(t *testing.T, frames []inferd.Response) *fakeInferd {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeInferd{t: t, addr: ln.Addr().String(), frames: frames, stop: make(chan struct{})}
	go func() {
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-f.stop:
					return
				default:
					return
				}
			}
			go f.serve(conn)
		}
	}()
	t.Cleanup(func() {
		close(f.stop)
		_ = ln.Close()
	})
	return f
}

func (f *fakeInferd) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	// Read one request line; then push frames.
	if _, err := r.ReadBytes('\n'); err != nil {
		if !errors.Is(err, io.EOF) {
			f.t.Logf("fake inferd read req: %v", err)
		}
		return
	}
	enc := json.NewEncoder(conn)
	for _, fr := range f.frames {
		if err := enc.Encode(fr); err != nil {
			f.t.Logf("fake inferd write frame: %v", err)
			return
		}
	}
}

func TestPostStreamCollapseTokenFrames(t *testing.T) {
	srv := newFakeInferd(t, []inferd.Response{
		{ID: "x", Type: inferd.ResponseToken, Content: "Hello "},
		{ID: "x", Type: inferd.ResponseToken, Content: "world"},
		{ID: "x", Type: inferd.ResponseToken, Content: "!"},
		{ID: "x", Type: inferd.ResponseDone, StopReason: inferd.StopEnd},
	})

	c := &Client{Address: srv.addr, UseTCP: true}
	got, err := c.Post(context.Background(), inferd.Request{
		ID: "x",
		Messages: []inferd.Message{
			{Role: inferd.RoleUser, Content: "say hi"},
		},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got != "Hello world!" {
		t.Errorf("collapsed text = %q; want %q", got, "Hello world!")
	}
}

func TestPostDoneOnlyFallsBackToDoneContent(t *testing.T) {
	// Some processors may return the whole response in the done frame's
	// Content rather than as token chunks. Verify we don't drop it.
	srv := newFakeInferd(t, []inferd.Response{
		{ID: "x", Type: inferd.ResponseDone, Content: "complete answer", StopReason: inferd.StopEnd},
	})

	c := &Client{Address: srv.addr, UseTCP: true}
	got, err := c.Post(context.Background(), inferd.Request{ID: "x"})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got != "complete answer" {
		t.Errorf("done-only collapse = %q; want %q", got, "complete answer")
	}
}

func TestPostErrorFrameSurfacesAsError(t *testing.T) {
	srv := newFakeInferd(t, []inferd.Response{
		{ID: "x", Type: inferd.ResponseError, Code: inferd.ErrQueueFull, Message: "too many in flight"},
	})

	c := &Client{Address: srv.addr, UseTCP: true}
	_, err := c.Post(context.Background(), inferd.Request{ID: "x"})
	if err == nil {
		t.Fatal("expected error on error frame, got nil")
	}
	if !strings.Contains(err.Error(), "queue_full") || !strings.Contains(err.Error(), "too many in flight") {
		t.Errorf("error message %q missing code/message detail", err)
	}
}

func TestPostBackendNotReadyOnConnectRefused(t *testing.T) {
	// 127.0.0.1:1 is virtually guaranteed to refuse connections —
	// no service binds port 1 on a normal machine.
	c := &Client{Address: "127.0.0.1:1", UseTCP: true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := c.Post(ctx, inferd.Request{ID: "x"})
	if err == nil {
		t.Fatal("expected error on refused connect")
	}
	if !errors.Is(err, ErrBackendNotReady) {
		t.Errorf("expected ErrBackendNotReady, got %v", err)
	}
}

func TestLooksLikeTCP(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"127.0.0.1:47320", true},
		{"localhost:8080", true},
		{"[::1]:9999", true},
		{"/run/user/1000/inferd/infer.sock", false},
		{`\\.\pipe\inferd-infer`, false},
		{"plain", false},
		{"trailing:", false},
		{"port-not-numeric:abc", false},
	}
	for _, tc := range cases {
		if got := looksLikeTCP(tc.in); got != tc.want {
			t.Errorf("looksLikeTCP(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsTransientConnectMatchesCommonRefusalShapes(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), true},
		{errors.New("open /run/inferd/infer.sock: no such file or directory"), true},
		{errors.New("CreateFile \\\\.\\pipe\\inferd-infer: The system cannot find the file specified."), true},
		{errors.New("All pipe instances are busy"), true},
		{errors.New("permission denied"), false},
		{errors.New("malformed address"), false},
	}
	for _, tc := range cases {
		if got := isTransientConnect(tc.err); got != tc.want {
			t.Errorf("isTransientConnect(%v) = %v; want %v", tc.err, got, tc.want)
		}
	}
}
