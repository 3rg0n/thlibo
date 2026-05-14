package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/3rg0n/thlibo/internal/ipc"
	"github.com/3rg0n/thlibo/internal/logx"
	"github.com/3rg0n/thlibo/internal/queue"
)

// MaxRestartAttempts is the spec's lifetime cap on llamafile restarts
// (A11). After the Nth crash (N = MaxRestartAttempts + 1), the daemon
// emits an error on the admin socket and stops spawning replacements.
const MaxRestartAttempts = 3

// Config wires a daemon instance. All fields are required unless marked
// optional. Defaults belong in the cmd/thlibod flag parser, not here -
// the lifecycle package owns correctness, not ergonomics.
type Config struct {
	LockPath          string
	EngineCmd         func() *exec.Cmd // factory so restarts build a fresh Cmd
	InferenceEndpoint ipc.EndpointConfig
	AdminEndpoint     ipc.EndpointConfig
	ReadyPollInterval time.Duration // default 50ms
	ReadyPollTimeout  time.Duration // default 30s
	StopTimeout       time.Duration // default 5s (time given to engine on shutdown)
	QueueDepth        int           // default 10 (spec §Concurrency)

	// Logger is optional; nil is safe (all logging calls are nil-guarded).
	// cmd/thlibod wires this to a logx.Logger so operators can see what
	// the daemon's doing.
	Logger *logx.Logger
}

// Daemon is the live instance. Build with Start, shut down with Stop.
type Daemon struct {
	cfg Config

	lock *Lock

	engineMu sync.RWMutex
	engine   *SubprocessEngine

	infL    net.Listener
	adminL  net.Listener
	adminMu sync.Mutex
	admins  []net.Conn // open admin connections, broadcast status to all

	q *queue.Queue

	restartAttempts atomic.Int32
	supervisorDone  chan struct{}
	engineDead      atomic.Bool

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
	if cfg.QueueDepth == 0 {
		cfg.QueueDepth = 10
	}
	if cfg.EngineCmd == nil {
		return nil, errors.New("daemon: EngineCmd is required")
	}

	d := &Daemon{
		cfg:            cfg,
		q:              queue.New(cfg.QueueDepth),
		supervisorDone: make(chan struct{}),
		ready:          make(chan struct{}),
		stopCh:         make(chan struct{}),
		stopped:        make(chan struct{}),
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
	d.setEngine(eng)

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

	// Step 6: engine supervisor (A11): restart on crash up to
	// MaxRestartAttempts, then error on admin and stop.
	// nosec G118: the supervisor is daemon-lifetime-scoped, not
	// request-scoped. Cancellation is driven by d.stopCh (closed in
	// Stop), not by the Start ctx which only gates startup.
	go d.superviseEngine() // #nosec G118

	// Step 7: watch for SIGTERM-equivalent via stopCh.
	go d.waitForStop()

	return d, nil
}

func (d *Daemon) setEngine(e *SubprocessEngine) {
	d.engineMu.Lock()
	d.engine = e
	d.engineMu.Unlock()
	d.engineDead.Store(false)
}

func (d *Daemon) currentEngine() *SubprocessEngine {
	d.engineMu.RLock()
	defer d.engineMu.RUnlock()
	return d.engine
}

// superviseEngine implements A11: on engine exit, if we are not
// shutting down, attempt up to MaxRestartAttempts restarts (lifetime
// counter). On the Nth failure (N > MaxRestartAttempts), broadcast an
// error status on admin and stop trying. Subsequent Generate calls
// will see engineDead and fail fast.
func (d *Daemon) superviseEngine() {
	defer close(d.supervisorDone)
	for {
		eng := d.currentEngine()
		if eng == nil {
			return
		}
		select {
		case <-eng.Done():
		case <-d.stopCh:
			return
		}

		// Normal shutdown: stopCh will close shortly; exit.
		select {
		case <-d.stopCh:
			return
		default:
		}

		attempts := d.restartAttempts.Add(1)
		if attempts > MaxRestartAttempts {
			d.engineDead.Store(true)
			d.broadcastAdmin(ipc.Response{
				ID:      ipc.AdminID,
				Type:    ipc.ResponseError,
				Message: fmt.Sprintf("engine exceeded restart limit (%d attempts)", MaxRestartAttempts),
			})
			return
		}

		d.broadcastAdmin(ipc.Response{
			ID:     ipc.AdminID,
			Type:   ipc.ResponseStatus,
			Status: fmt.Sprintf("restarting_engine_attempt_%d", attempts),
		})

		newEng, err := StartSubprocessEngine(d.cfg.EngineCmd())
		if err != nil {
			d.broadcastAdmin(ipc.Response{
				ID:      ipc.AdminID,
				Type:    ipc.ResponseError,
				Message: fmt.Sprintf("engine restart failed: %v", err),
			})
			continue // loop; restartAttempts already incremented
		}
		d.setEngine(newEng)

		// Wait for the restarted engine to become ready, bounded by the
		// ready-poll timeout. If it never does, treat as a crash.
		readyCtx, cancel := context.WithTimeout(context.Background(), d.cfg.ReadyPollTimeout)
		err = d.waitReady(readyCtx)
		cancel()
		if err != nil {
			_ = newEng.Stop(d.cfg.StopTimeout)
			continue
		}
		d.broadcastAdmin(ipc.Response{
			ID:     ipc.AdminID,
			Type:   ipc.ResponseStatus,
			Status: "ready",
		})
	}
}

// broadcastAdmin sends frame to every currently-connected admin
// client. Failures drop the client from the list.
func (d *Daemon) broadcastAdmin(frame ipc.Response) {
	d.adminMu.Lock()
	defer d.adminMu.Unlock()
	alive := d.admins[:0]
	for _, c := range d.admins {
		if err := ipc.WriteFrame(c, frame); err != nil {
			_ = c.Close()
			continue
		}
		alive = append(alive, c)
	}
	d.admins = alive
}

func (d *Daemon) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(d.cfg.ReadyPollTimeout)
	t := time.NewTicker(d.cfg.ReadyPollInterval)
	defer t.Stop()
	for {
		eng := d.currentEngine()
		if eng == nil {
			return errors.New("daemon: no engine")
		}
		if eng.Ready() {
			return nil
		}
		select {
		case <-eng.Done():
			return fmt.Errorf("daemon: engine exited before ready: %w", eng.ExitErr())
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
	if eng := d.currentEngine(); eng == nil || !eng.Ready() {
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

// handleInference reads a request, submits it to the queue, and streams
// tokens back to the client. Client disconnect cancels the job's
// context (A9). Queue full returns an immediate error frame (A8).
func (d *Daemon) handleInference(conn net.Conn) {
	defer conn.Close()
	start := time.Now()

	// Defence-in-depth identity check at accept time. See
	// THREAT_MODEL.md finding #24. Primary identity gate is still
	// the socket ACL (0660 group thlibo-users on Unix; user-SID-only
	// SDDL on Windows). A mismatch here means either a group-
	// membership misconfiguration or an active attempt to talk to
	// the daemon from a different identity; either way we reject.
	peer, perr := ipc.PeerIdentity(conn)
	if perr != nil && !errors.Is(perr, ipc.ErrNoPeerIdentity) {
		d.cfg.Logger.Warn("peer_identity_failed", logx.Err(perr))
		// Don't reject; if we can't read the peer we still honour
		// the socket ACL, which is the primary gate. Log and
		// continue with an empty caller ID (skips per-caller quota).
	}
	if !d.peerAllowed(peer) {
		d.cfg.Logger.Warn("peer_rejected",
			logx.Str("transport", peer.Transport),
			logx.Int("peer_uid", peer.UID),
			logx.Str("peer_sid", peer.SID))
		_ = ipc.WriteFrame(conn, ipc.Response{
			Type: ipc.ResponseError, Message: "peer identity mismatch",
		})
		return
	}

	r := bufio.NewReader(conn)
	// ipc.ReadRequest returns (Request, error) by value — req is
	// safe to address on the error path (it's the zero value).
	// nosemgrep: trailofbits.go.invalid-usage-of-modified-variable.invalid-usage-of-modified-variable
	req, err := ipc.ReadRequest(r)
	if err != nil {
		if err == io.EOF {
			return
		}
		d.cfg.Logger.Warn("request_parse_failed", logx.Err(err))
		_ = ipc.WriteFrame(conn, ipc.Response{
			ID: req.ID, Type: ipc.ResponseError, Message: err.Error(),
		})
		return
	}

	if d.engineDead.Load() {
		d.cfg.Logger.Warn("engine_dead_refusing_request", logx.Str("req_id", req.ID))
		_ = ipc.WriteFrame(conn, ipc.Response{
			ID: req.ID, Type: ipc.ResponseError, Message: "engine unavailable",
		})
		return
	}

	resolved, err := req.Resolve()
	if err != nil {
		d.cfg.Logger.Warn("request_validation_failed",
			logx.Str("req_id", req.ID),
			logx.Err(err),
		)
		_ = ipc.WriteFrame(conn, ipc.Response{
			ID: req.ID, Type: ipc.ResponseError, Message: err.Error(),
		})
		return
	}
	d.cfg.Logger.Info("request_accepted",
		logx.Str("req_id", req.ID),
		logx.Int("msgs", len(req.Messages)),
		logx.Int("max_tokens", resolved.MaxTokens),
		logx.Bool("has_grammar", resolved.Grammar != ""),
	)
	_ = start

	// Split messages into system / user (the engine takes two distinct
	// slots; the protocol allows multiple messages but v0.1 is single-
	// turn per spec §Gemma 4 E4B reference).
	prompt := GeneratePrompt{
		Temperature: resolved.Temperature,
		TopP:        resolved.TopP,
		TopK:        resolved.TopK,
		MaxTokens:   resolved.MaxTokens,
	}
	for _, m := range req.Messages {
		switch m.Role {
		case ipc.RoleSystem:
			prompt.System = m.Content
		case ipc.RoleUser:
			prompt.User = m.Content
		}
	}

	// ctx ties the job's lifetime to the client connection. We detect
	// disconnect by reading off the connection: a Read returning
	// EOF/error triggers cancel, which propagates into the running
	// Generate via Job.Ctx.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchDisconnect(conn, cancel)

	tokens := make(chan string, 16)
	var runErr error
	var usage ipc.Usage

	job := &queue.Job{
		Ctx:      ctx,
		CallerID: peer.String(),
		Run: func(jobCtx context.Context) {
			eng := d.currentEngine()
			if eng == nil || !eng.Ready() {
				runErr = ErrNotReady
				return
			}

			// Generate owns the tokens channel: we wait for it to
			// return before closing. The spawn+wait pattern lets us
			// relay tokens to the client in this goroutine while
			// Generate drives the child process in its own.
			generateDone := make(chan struct{})
			go func() {
				defer close(generateDone)
				if err := eng.Generate(jobCtx, prompt, tokens); err != nil {
					runErr = err
				}
			}()

			completion := 0
			for {
				select {
				case tok, ok := <-tokens:
					if !ok {
						// Channel unexpectedly closed without Generate
						// returning; defensive - treat as done.
						<-generateDone
						close(tokens)
						usage = ipc.Usage{
							PromptTokens:     countTokens(prompt),
							CompletionTokens: completion,
						}
						return
					}
					if werr := ipc.WriteFrame(conn, ipc.Response{
						ID: req.ID, Type: ipc.ResponseToken, Content: tok,
					}); werr != nil {
						cancel()
						<-generateDone
						close(tokens)
						return
					}
					completion++
				case <-generateDone:
					// Generate returned; drain any buffered tokens.
					for {
						select {
						case tok := <-tokens:
							_ = ipc.WriteFrame(conn, ipc.Response{
								ID: req.ID, Type: ipc.ResponseToken, Content: tok,
							})
							completion++
						default:
							close(tokens)
							usage = ipc.Usage{
								PromptTokens:     countTokens(prompt),
								CompletionTokens: completion,
							}
							return
						}
					}
				}
			}
		},
	}

	if err := d.q.Submit(job); err != nil {
		msg := err.Error()
		switch {
		case errors.Is(err, queue.ErrFull):
			msg = "queue full"
		case errors.Is(err, queue.ErrCallerFull):
			msg = "caller quota exceeded"
		}
		d.cfg.Logger.Warn("queue_rejected",
			logx.Str("req_id", req.ID),
			logx.Str("reason", msg),
			logx.Str("caller", peer.String()))
		_ = ipc.WriteFrame(conn, ipc.Response{
			ID: req.ID, Type: ipc.ResponseError, Message: msg,
		})
		return
	}

	<-job.Done

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		d.cfg.Logger.Warn("request_failed",
			logx.Str("req_id", req.ID),
			logx.Err(runErr),
			logx.Dur("duration", time.Since(start)),
		)
		_ = ipc.WriteFrame(conn, ipc.Response{
			ID: req.ID, Type: ipc.ResponseError, Message: runErr.Error(),
		})
		return
	}
	if job.Dropped() {
		d.cfg.Logger.Info("request_cancelled",
			logx.Str("req_id", req.ID),
			logx.Dur("duration", time.Since(start)),
		)
		return
	}
	d.cfg.Logger.Info("request_done",
		logx.Str("req_id", req.ID),
		logx.Int("prompt_tokens", usage.PromptTokens),
		logx.Int("completion_tokens", usage.CompletionTokens),
		logx.Dur("duration", time.Since(start)),
	)
	_ = ipc.WriteFrame(conn, ipc.Response{
		ID: req.ID, Type: ipc.ResponseDone, Usage: &usage,
	})
}

// watchDisconnect blocks on Read and calls cancel when the client goes
// away. The spec expects cancellation within 500ms of disconnect (A9);
// a blocking Read on a closed/half-closed socket returns essentially
// immediately, so we inherit that latency.
func watchDisconnect(conn net.Conn, cancel context.CancelFunc) {
	buf := make([]byte, 16)
	for {
		if _, err := conn.Read(buf); err != nil {
			cancel()
			return
		}
	}
}

// countTokens is a rough character-based approximation of prompt token
// count for the response's usage field. Real counting would require
// the model tokenizer; for v0.1 we report a stable estimate. The spec
// does not require accuracy here - the usage field is informational.
func countTokens(p GeneratePrompt) int {
	return (len(p.System) + len(p.User)) / 4
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

	// Drain any queued or running jobs.
	if d.q != nil {
		d.q.Close()
	}

	// Close all admin connections cleanly.
	d.adminMu.Lock()
	for _, c := range d.admins {
		_ = c.Close()
	}
	d.admins = nil
	d.adminMu.Unlock()

	// Stop the engine child.
	if eng := d.currentEngine(); eng != nil {
		_ = eng.Stop(d.cfg.StopTimeout)
	}

	// Wait for the restart supervisor to exit.
	<-d.supervisorDone

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

// peerAllowed is the second-layer identity check at accept. It's
// complementary to the socket ACL: the ACL keeps out the wrong
// users at the kernel level, and this check catches the case where
// the ACL is misconfigured (e.g. thlibo-users group contains more
// members than the operator intended) or a compromised process with
// legitimate group membership is trying to act on another user's
// behalf.
//
// Rules:
//   - tcp transport: allow (TCP loopback has no kernel-enforced
//     identity; operators expose it explicitly when they trust the
//     host).
//   - unix transport with peer.UID == -1: allow (darwin stub path
//     until LOCAL_PEERCRED is wrapped in v0.3).
//   - unix: allow iff peer.UID == os.Geteuid().
//   - windows: allow iff peer.SID matches the daemon's own user
//     SID. We compute the daemon SID lazily on first check.
//   - empty transport / no peer: allow (ErrNoPeerIdentity already
//     logged upstream).
//
// See THREAT_MODEL.md finding #24.
func (d *Daemon) peerAllowed(peer ipc.PeerID) bool {
	switch peer.Transport {
	case "":
		return true // no identity — rely on ACL
	case "tcp":
		return true // operator-chosen loopback; API-key auth is v0.3
	case "unix":
		if peer.UID < 0 {
			return true // darwin stub — allow, ACL is primary gate
		}
		return peer.UID == os.Geteuid()
	case "windows":
		ok, err := daemonSIDMatches(peer.SID)
		if err != nil {
			// If we can't resolve our own SID, log and fall back to
			// the ACL (primary gate) by allowing the connection.
			d.cfg.Logger.Warn("daemon_sid_lookup_failed", logx.Err(err))
			return true
		}
		return ok
	default:
		return true
	}
}
