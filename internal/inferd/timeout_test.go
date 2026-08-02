package inferd

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// TestPostBoundedAgainstWedgedDaemon is the regression test for the
// unbounded-context hang: a daemon that accepts the connection and then
// never answers used to stall Post forever, because every hot-path
// subcommand passes context.Background(). ADR 0006 promises fail-open;
// an unbounded wait is a hang, not a fallback.
func TestPostBoundedAgainstWedgedDaemon(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })

	// Server reads the request and deliberately never replies.
	go func() {
		buf := make([]byte, 4096)
		_, _ = c2.Read(buf)
	}()

	cl := &Client{
		Timeout:  150 * time.Millisecond,
		dialFunc: func(context.Context) (net.Conn, error) { return c1, nil },
	}

	done := make(chan error, 1)
	go func() {
		_, err := cl.Post(context.Background(), Request{ID: "wedged"})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want context.DeadlineExceeded, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Post did not return: a wedged daemon still stalls the AI client")
	}
}

// A caller's own shorter deadline must survive — WithTimeout only ever
// tightens, so the client's bound must not extend a tighter caller ctx.
func TestPostRespectsTighterCallerDeadline(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })
	go func() {
		buf := make([]byte, 4096)
		_, _ = c2.Read(buf)
	}()

	cl := &Client{
		Timeout:  30 * time.Second, // generous client bound
		dialFunc: func(context.Context) (net.Conn, error) { return c1, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := cl.Post(ctx, Request{ID: "tight"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("caller deadline ignored: took %s", elapsed)
	}
}

// Timeout < 0 restores unbounded behaviour for operators debugging a
// slow local model. Verified by observing that the call is still running
// after a bound that would have fired.
func TestNegativeTimeoutDisablesDeadline(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })
	go func() {
		buf := make([]byte, 4096)
		_, _ = c2.Read(buf)
	}()

	cl := &Client{
		Timeout:  -1,
		dialFunc: func(context.Context) (net.Conn, error) { return c1, nil },
	}

	done := make(chan struct{})
	go func() {
		_, _ = cl.Post(context.Background(), Request{ID: "unbounded"})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Timeout=-1 should not impose a deadline, but Post returned")
	case <-time.After(300 * time.Millisecond):
		// Still blocked, as requested.
	}
}

// A dead daemon plus an expired context is both an unreachable backend
// and a timeout. ErrBackendNotReady must survive: it is the fail-open
// signal callers switch on (ADR 0006) and the more actionable of the two
// classifications. Relabelling it as a timeout would report a
// daemon-down event as slowness.
func TestBackendNotReadySurvivesExpiredContext(t *testing.T) {
	cl := &Client{
		Timeout: 50 * time.Millisecond,
		dialFunc: func(ctx context.Context) (net.Conn, error) {
			// Outlive the deadline, then fail the way a down daemon does.
			<-ctx.Done()
			return nil, errors.New("connect: connection refused")
		},
	}
	_, err := cl.Post(context.Background(), Request{ID: "down"})
	if !errors.Is(err, ErrBackendNotReady) {
		t.Fatalf("want ErrBackendNotReady preserved, got %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("daemon-down must not be relabelled a timeout: %v", err)
	}
}

func TestResolveTimeout(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"", DefaultTimeout},
		{"30s", 30 * time.Second},
		{"2m", 2 * time.Minute},
		{"45", 45 * time.Second},    // bare integer = seconds
		{"0", 0},                    // explicit disable
		{"garbage", DefaultTimeout}, // malformed must not break the middleware
		{"-5s", DefaultTimeout},     // negative duration rejected
	}
	for _, c := range cases {
		t.Setenv(TimeoutEnv, c.env)
		if c.env == "" {
			// t.Setenv can't unset; emulate an absent variable.
			t.Setenv(TimeoutEnv, "")
		}
		if got := resolveTimeout(); got != c.want {
			t.Errorf("resolveTimeout(%q) = %v, want %v", c.env, got, c.want)
		}
	}
}

// An explicit Client.Timeout must win over the environment.
func TestClientTimeoutOverridesEnv(t *testing.T) {
	t.Setenv(TimeoutEnv, "99s")
	cl := &Client{Timeout: 3 * time.Second}
	if got := cl.timeout(); got != 3*time.Second {
		t.Errorf("Client.Timeout should win over env: got %v", got)
	}
	envOnly := &Client{}
	if got := envOnly.timeout(); got != 99*time.Second {
		t.Errorf("env should apply when Client.Timeout is zero: got %v", got)
	}
}
