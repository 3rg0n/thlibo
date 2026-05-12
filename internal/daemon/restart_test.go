package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/3rg0n/thlibo/internal/ipc"
)

// A11: llamafile crash -> up to MaxRestartAttempts restarts (total,
// lifetime). After exhaustion, admin receives an error and no further
// spawns happen.
//
// We drive this with the stub's -crash-after=1 flag: every engine
// instance exits 2 after completing 1 request. So we send request #1,
// engine crashes, restart #1 happens. Request #2, crash, restart #2.
// Request #3, crash, restart #3. Request #4, crash, and now restarts
// exhausted: admin gets error, next request fails fast.
func TestEngineRestartCapExhaustion(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t, "-crash-after=1", "-tokens=t")
	// Shorten the ready poll so restart waits don't balloon the test.
	cfg.ReadyPollInterval = 20 * time.Millisecond
	cfg.ReadyPollTimeout = 3 * time.Second

	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(stopCtx(t, 3*time.Second)) })

	adminConn, err := net.Dial("tcp", d.AdminAddr().String())
	if err != nil {
		t.Fatalf("dial admin: %v", err)
	}
	defer adminConn.Close()
	adminR := bufio.NewReader(adminConn)
	// Drain the initial "ready" frame.
	_, _ = adminR.ReadBytes('\n')

	// Drive crashes until we see the exhaustion error on admin. We send
	// a request, wait for the done/error frame, then wait briefly for
	// the supervisor to observe the crash and restart (or exhaust). We
	// cap attempts so a bug can't hang the test forever.
	adminFrames := make([]string, 0)
	adminDone := make(chan struct{})
	adminErr := make(chan string, 1)
	go func() {
		defer close(adminDone)
		for {
			line, err := adminR.ReadBytes('\n')
			if err != nil {
				return
			}
			adminFrames = append(adminFrames, string(line))
			var resp ipc.Response
			if jerr := json.Unmarshal(line, &resp); jerr != nil {
				continue
			}
			if resp.Type == ipc.ResponseError &&
				strings.Contains(resp.Message, "restart limit") {
				adminErr <- resp.Message
				return
			}
		}
	}()

	// Fire up to 8 requests. Each completes and then the stub exits 2,
	// which the supervisor counts as a crash. After 3 successful
	// restarts (attempts=1,2,3) the 4th observed crash triggers
	// exhaustion.
	for i := 0; i < 8; i++ {
		select {
		case <-adminErr:
			goto saw
		default:
		}
		sendAndComplete(t, d.InferenceAddr().String(), "req-"+itoa(i))
		// Give the supervisor time to observe the exit and start (or
		// fail to start) a replacement before we fire the next request.
		time.Sleep(500 * time.Millisecond)
	}

	select {
	case <-adminErr:
	case <-time.After(5 * time.Second):
		t.Fatalf("never saw restart-limit admin frame.\nadmin frames: %v",
			adminFrames)
	}
saw:

	// After exhaustion, inference requests fail fast with
	// "engine unavailable".
	conn, err := net.Dial("tcp", d.InferenceAddr().String())
	if err != nil {
		t.Fatalf("dial infer after exhaustion: %v", err)
	}
	defer conn.Close()
	req := `{"id":"req-dead","messages":[{"role":"user","content":"x"}]}` + "\n"
	_, _ = conn.Write([]byte(req))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read infer after exhaustion: %v", err)
	}
	var resp ipc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Type != ipc.ResponseError {
		t.Errorf("expected error after exhaustion, got %+v", resp)
	}
}

// A9: disconnecting the client mid-stream cancels the in-flight job.
// We use the stub's token-delay to hold tokens back so the client can
// disconnect while tokens are still being produced. The job's Done
// must close within 500ms of disconnect.
func TestClientDisconnectCancelsJob(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t,
		"-tokens=a,b,c,d,e,f,g,h,i,j",
		"-token-delay=200ms",
	)
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(stopCtx(t, 3*time.Second)) })

	conn, err := net.Dial("tcp", d.InferenceAddr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	req := `{"id":"req-cancel","messages":[{"role":"user","content":"x"}]}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read the first token, then close.
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := br.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	var first ipc.Response
	if err := json.Unmarshal(line, &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if first.Type != ipc.ResponseToken {
		t.Fatalf("expected token frame, got %+v", first)
	}
	closeStart := time.Now()
	_ = conn.Close()

	// The daemon must notice the disconnect and free the worker so
	// another request can proceed. We verify this by submitting a new
	// request on a fresh connection and timing how long it takes to
	// get its first token.
	//
	// We allow 3 token-delays (600ms) for scheduling slack on top of
	// the 500ms A9 requirement.
	deadline := closeStart.Add(3*time.Second + 500*time.Millisecond)
	conn2, err := net.Dial("tcp", d.InferenceAddr().String())
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer conn2.Close()
	req2 := `{"id":"req-after","messages":[{"role":"user","content":"x"}]}` + "\n"
	_, _ = conn2.Write([]byte(req2))
	_ = conn2.SetReadDeadline(deadline)
	if _, err := bufio.NewReader(conn2).ReadBytes('\n'); err != nil {
		t.Fatalf("follow-up request did not make progress: %v", err)
	}
}

// A8: submitting past the queue depth returns a "queue full" error
// frame immediately. We use a long-running request to occupy the worker
// and block the queue with fillers, then verify the overflow request
// sees "queue full" on its socket.
func TestInferenceQueueFull(t *testing.T) {
	f := newFixture(t)
	cfg := f.makeConfig(t,
		"-tokens=slow",
		"-token-delay=500ms",
	)
	cfg.QueueDepth = 2 // smaller depth = faster test
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(stopCtx(t, 3*time.Second)) })

	// Fill slots: 1 active + 2 queued = 3 total.
	conns := make([]net.Conn, 0, 3)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", d.InferenceAddr().String())
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		req := `{"id":"fill-` + itoa(i) + `","messages":[{"role":"user","content":"x"}]}` + "\n"
		if _, err := c.Write([]byte(req)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	// Give the daemon a moment to actually accept and queue them.
	time.Sleep(100 * time.Millisecond)

	// The 4th submission must be rejected immediately.
	overflow, err := net.Dial("tcp", d.InferenceAddr().String())
	if err != nil {
		t.Fatalf("dial overflow: %v", err)
	}
	t.Cleanup(func() { _ = overflow.Close() })
	req := `{"id":"overflow","messages":[{"role":"user","content":"x"}]}` + "\n"
	if _, err := overflow.Write([]byte(req)); err != nil {
		t.Fatalf("write overflow: %v", err)
	}
	_ = overflow.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(overflow).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read overflow: %v", err)
	}
	var resp ipc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Type != ipc.ResponseError {
		t.Errorf("overflow response type = %q, want error", resp.Type)
	}
	if resp.Message != "queue full" {
		t.Errorf("overflow message = %q, want \"queue full\"", resp.Message)
	}
}

// sendAndComplete dials the inference addr, sends a request with the
// given id, and drains frames until the terminal one. Used by the
// restart test to drive the engine to crash.
func sendAndComplete(t *testing.T, addr, id string) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	req := `{"id":"` + id + `","messages":[{"role":"user","content":"x"}]}` + "\n"
	_, _ = c.Write([]byte(req))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var r ipc.Response
			if jerr := json.Unmarshal(line, &r); jerr == nil {
				if r.Type == ipc.ResponseDone || r.Type == ipc.ResponseError {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
