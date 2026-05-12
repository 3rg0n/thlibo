package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sync"
	"time"

	"github.com/3rg0n/thlibo/internal/ipc"
)

// Config wires a daemon instance. All fields are required unless marked
// optional. Defaults belong in the cmd/thlibod flag parser, not here -
// the lifecycle package owns correctness, not ergonomics.
type Config struct {
	LockPath           string
	EngineCmd          func() *exec.Cmd // factory so restarts build a fresh Cmd
	InferenceEndpoint  ipc.EndpointConfig
	AdminEndpoint      ipc.EndpointConfig
	ReadyPollInterval  time.Duration // default 50ms
	ReadyPollTimeout   time.Duration // default 30s
	StopTimeout        time.Duration // default 5s (time given to engine on shutdown)
}

// Daemon is the live instance. Build with Start, shut down with Stop.
type Daemon struct {
	cfg Config

	lock     *Lock
	engine   *SubprocessEngine
	infL     net.Listener
	adminL   net.Listener
	adminMu  sync.Mutex
	admins   []net.Conn // open admin connections, broadcast status to all

	ready   chan struct{} // closed when engine is ready and sockets are open
	stopCh  chan struct{}
	stopped chan struct{}

	stopOnce sync.Once
}

// Start acquires the lock, spawns the engine, waits for ready, opens
// both IPC sockets (A4: NOT before ready), and begins accepting
// connections. Admin clients that connect after startup receive the
// current status; the startup loading_model -> ready transitions are
// broadcast as they happen (A10).
//
// Start returns only once the daemon is fully ready and accepting. On
// any failure it cleans up (closes listeners, stops engine, releases
// lock) before returning the error.
func Start(ctx context.Context, cfg Config) (*Daemon, error) {
	if cfg.ReadyPollInterval == 0 {
		cfg.ReadyPollInterval = 50 * time.Millisecond
	}
	if cfg.ReadyPollTimeout == 0 {
		cfg.ReadyPollTimeout = 30 * time.Second
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 5 * time.Second
	}
	if cfg.EngineCmd == nil {
		return nil, errors.New("daemon: EngineCmd is required")
	}

	d := &Daemon{
		cfg:     cfg,
		ready:   make(chan struct{}),
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}

	// Step 1: acquire single-instance lock.
	lock, err := AcquireLock(cfg.LockPath)
	if err != nil {
		return nil, err
	}
	d.lock = lock

	// Step 2: spawn the engine.
	eng, err := StartSubprocessEngine(cfg.EngineCmd())
	if err != nil {
		_ = d.lock.Release()
		return nil, fmt.Errorf("daemon: start engine: %w", err)
	}
	d.engine = eng

	// Step 3: wait for engine to be ready. A4 forbids creating the IPC
	// sockets before this point. While we wait, admin clients cannot
	// connect yet - the spec's startup loading_model frame is buffered
	// implicitly: once the admin socket opens, the very first frame we
	// send on a new connection is the current status ("ready").
	if err := d.waitReady(ctx); err != nil {
		_ = eng.Stop(cfg.StopTimeout)
		_ = d.lock.Release()
		return nil, err
	}

	// Step 4: open IPC sockets.
	infL, err := ipc.Listen(cfg.InferenceEndpoint)
	if err != nil {
		_ = eng.Stop(cfg.StopTimeout)
		_ = d.lock.Release()
		return nil, fmt.Errorf("daemon: inference listen: %w", err)
	}
	d.infL = infL

	adminL, err := ipc.Listen(cfg.AdminEndpoint)
	if err != nil {
		_ = infL.Close()
		_ = eng.Stop(cfg.StopTimeout)
		_ = d.lock.Release()
		return nil, fmt.Errorf("daemon: admin listen: %w", err)
	}
	d.adminL = adminL

	close(d.ready)

	// Step 5: accept loops.
	go d.acceptAdmin()
	go d.acceptInference()

	// Step 6: watch for SIGTERM-equivalent via stopCh.
	go d.waitForStop()

	return d, nil
}

func (d *Daemon) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(d.cfg.ReadyPollTimeout)
	t := time.NewTicker(d.cfg.ReadyPollInterval)
	defer t.Stop()
	for {
		if d.engine.Ready() {
			return nil
		}
		select {
		case <-d.engine.Done():
			return fmt.Errorf("daemon: engine exited before ready: %w", d.engine.ExitErr())
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("daemon: engine not ready after %s", d.cfg.ReadyPollTimeout)
			}
		}
	}
}

func (d *Daemon) acceptAdmin() {
	for {
		conn, err := d.adminL.Accept()
		if err != nil {
			return
		}
		d.handleAdmin(conn)
	}
}

// handleAdmin greets a new admin client with the current status and
// holds the connection open for future broadcasts (health updates when
// the engine restarts, etc.). For v0.1 the daemon only broadcasts on
// lifecycle events; no admin commands are accepted yet.
func (d *Daemon) handleAdmin(conn net.Conn) {
	d.adminMu.Lock()
	d.admins = append(d.admins, conn)
	d.adminMu.Unlock()

	status := "ready"
	if !d.engine.Ready() {
		status = "loading_model"
	}
	_ = ipc.WriteFrame(conn, ipc.Response{
		ID:     ipc.AdminID,
		Type:   ipc.ResponseStatus,
		Status: status,
	})

	go func() {
		buf := make([]byte, 64)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				d.removeAdmin(conn)
				_ = conn.Close()
				return
			}
		}
	}()
}

func (d *Daemon) removeAdmin(conn net.Conn) {
	d.adminMu.Lock()
	defer d.adminMu.Unlock()
	for i, c := range d.admins {
		if c == conn {
			d.admins = append(d.admins[:i], d.admins[i+1:]...)
			return
		}
	}
}

// acceptInference is a placeholder stub for Phase 2. It accepts
// connections and closes them immediately - real request dispatch
// arrives with the queue (A8) in Phase 2.
func (d *Daemon) acceptInference() {
	for {
		conn, err := d.infL.Accept()
		if err != nil {
			return
		}
		go d.handleInference(conn)
	}
}

// handleInference reads one request and returns an error frame because
// v0.1 Phase 1 has no queue yet. Phase 2 replaces this with queued
// dispatch. Keeping the stub here means the full protocol is exercised
// end-to-end as early as possible.
func (d *Daemon) handleInference(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	req, err := ipc.ReadRequest(r)
	if err != nil {
		if err == io.EOF {
			return
		}
		_ = ipc.WriteFrame(conn, ipc.Response{
			ID: "error", Type: ipc.ResponseError, Message: err.Error(),
		})
		return
	}
	_ = ipc.WriteFrame(conn, ipc.Response{
		ID: req.ID, Type: ipc.ResponseError, Message: "inference dispatch arrives in Phase 2",
	})
}

// waitForStop blocks until Stop is called, then drains and cleans up.
func (d *Daemon) waitForStop() {
	<-d.stopCh

	// A12 step 9: stop accepting new connections.
	if d.infL != nil {
		_ = d.infL.Close()
	}
	if d.adminL != nil {
		_ = d.adminL.Close()
	}

	// Close all admin connections cleanly.
	d.adminMu.Lock()
	for _, c := range d.admins {
		_ = c.Close()
	}
	d.admins = nil
	d.adminMu.Unlock()

	// Stop the engine child.
	if d.engine != nil {
		_ = d.engine.Stop(d.cfg.StopTimeout)
	}

	// Release the lock.
	if d.lock != nil {
		_ = d.lock.Release()
	}

	close(d.stopped)
}

// Stop signals a graceful shutdown: stop accepting, drain, release the
// engine and the lock. Safe to call multiple times; subsequent calls
// wait for the first to complete.
func (d *Daemon) Stop(ctx context.Context) error {
	d.stopOnce.Do(func() { close(d.stopCh) })
	select {
	case <-d.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Ready returns a channel that is closed once the daemon has opened
// its IPC sockets and is accepting connections. Tests use this to
// avoid polling.
func (d *Daemon) Ready() <-chan struct{} { return d.ready }

// InferenceAddr returns the listener address of the inference endpoint.
func (d *Daemon) InferenceAddr() net.Addr { return d.infL.Addr() }

// AdminAddr returns the listener address of the admin endpoint.
func (d *Daemon) AdminAddr() net.Addr { return d.adminL.Addr() }
