package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Engine represents the external inference process (llamafile in
// production; a test stub in unit tests). The daemon never speaks to the
// engine directly once spawned - it submits requests and reads tokens
// through this interface.
//
// The engine is intentionally minimal. All prompt assembly, sampling
// config, and protocol framing lives in the daemon/ipc layer above.
// Engine.Generate takes an already-resolved prompt and produces tokens;
// it knows nothing about JSON wire format, queueing, or clients.
type Engine interface {
	// Ready reports whether the engine is done loading and can serve
	// Generate calls.
	Ready() bool

	// Generate streams tokens for prompt into tokens. Closing the
	// tokens channel signals completion. Context cancellation stops
	// generation promptly (spec A9: client disconnect cancels job).
	Generate(ctx context.Context, prompt GeneratePrompt, tokens chan<- string) error

	// Stop asks the engine to shut down gracefully, waiting up to
	// timeout for a clean exit before forcing termination.
	Stop(timeout time.Duration) error

	// Done returns a channel closed when the engine process exits.
	// Used by the crash-restart supervisor.
	Done() <-chan struct{}
}

// GeneratePrompt is the minimum the daemon hands to the engine per
// request. All sampling params travel alongside.
type GeneratePrompt struct {
	System      string
	User        string
	Temperature float64
	TopP        float64
	TopK        int
	MaxTokens   int
}

// ErrNotReady is returned when Generate is called before the engine has
// finished loading.
var ErrNotReady = errors.New("daemon: engine not ready")

// SubprocessEngine is the shared implementation used by both the real
// llamafile driver and the test stub. It spawns a child process,
// streams a "READY" sentinel on stderr to flip Ready() true, then
// accepts one prompt per request on stdin and reads tokens from stdout
// until an END-OF-STREAM sentinel.
//
// Wire protocol to the child (kept intentionally simple so the stub is
// easy to write):
//
//	stdin:   one request per line, JSON: {"system":"...","user":"...",
//	         "temperature":1.0,"top_p":0.95,"top_k":64,"max_tokens":1000}
//	stdout:  token lines, each line is one token chunk, followed by
//	         a literal line containing exactly "<<END>>" to mark end of
//	         response. The daemon never emits "<<END>>" as a real token.
//	stderr:  diagnostics. The literal line "READY" on stderr switches
//	         the engine into Ready state.
//
// A real llamafile wrapper would translate its stdout into this shape;
// for v0.1 the daemon only talks to children that implement this
// protocol.
type SubprocessEngine struct {
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bufio.Reader

	ready atomic.Bool
	done  chan struct{}
	exit  error

	// genMu serialises Generate calls. The engine protocol above is
	// single-request at a time; the daemon's queue enforces that
	// above this layer, but we double-lock here to fail fast if
	// someone calls Generate concurrently by mistake.
	genMu sync.Mutex
}

// StartSubprocessEngine launches cmd as a child and returns a started
// engine. The caller must call Stop when done.
func StartSubprocessEngine(cmd *exec.Cmd) (*SubprocessEngine, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("engine: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("engine: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("engine: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("engine: start: %w", err)
	}

	e := &SubprocessEngine{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
		stderr: bufio.NewReader(stderrPipe),
		done:   make(chan struct{}),
	}
	go e.watchStderr()
	go e.watchExit()
	return e, nil
}

func (e *SubprocessEngine) watchStderr() {
	for {
		line, err := e.stderr.ReadString('\n')
		if line != "" {
			if strings.TrimSpace(line) == "READY" {
				e.ready.Store(true)
			}
		}
		if err != nil {
			return
		}
	}
}

func (e *SubprocessEngine) watchExit() {
	e.exit = e.cmd.Wait()
	e.ready.Store(false)
	close(e.done)
}

func (e *SubprocessEngine) Ready() bool { return e.ready.Load() }

func (e *SubprocessEngine) Done() <-chan struct{} { return e.done }

// ExitErr returns the exit error (nil if the process is still running
// or exited cleanly). Valid only after Done fires.
func (e *SubprocessEngine) ExitErr() error { return e.exit }

// Generate sends prompt to the child and streams tokens until the
// child emits the end-of-stream sentinel or ctx is cancelled. Each
// token arrives on the channel stripped of its trailing newline.
//
// On ctx cancellation, the child is signalled via Stop so tokens stop
// flowing. Callers handle the queue advance above this layer.
func (e *SubprocessEngine) Generate(ctx context.Context, prompt GeneratePrompt, tokens chan<- string) error {
	if !e.ready.Load() {
		return ErrNotReady
	}
	e.genMu.Lock()
	defer e.genMu.Unlock()

	// Build the child request via json.Marshal, not fmt.Sprintf(%q).
	// %q uses Go's string-literal escape rules, which differ from JSON
	// for control characters (e.g. U+2028 / U+2029) and for raw
	// backslash/quote edge cases in non-ASCII text. json.Marshal
	// produces spec-correct JSON regardless of the input bytes. See
	// THREAT_MODEL.md finding #18.
	buf, err := json.Marshal(struct {
		System      string  `json:"system"`
		User        string  `json:"user"`
		Temperature float64 `json:"temperature"`
		TopP        float64 `json:"top_p"`
		TopK        int     `json:"top_k"`
		MaxTokens   int     `json:"max_tokens"`
	}{
		System: prompt.System, User: prompt.User,
		Temperature: prompt.Temperature, TopP: prompt.TopP,
		TopK: prompt.TopK, MaxTokens: prompt.MaxTokens,
	})
	if err != nil {
		return fmt.Errorf("engine: marshal prompt: %w", err)
	}
	buf = append(buf, '\n')
	if _, err := e.stdin.Write(buf); err != nil {
		return fmt.Errorf("engine: write prompt: %w", err)
	}

	// Reader goroutine drains stdout until <<END>> or error; sends each
	// token into the channel unless ctx is already cancelled. Generate
	// does NOT return until this goroutine exits - otherwise a caller
	// closing `tokens` on Generate's return would race with a pending
	// send.
	readDone := make(chan error, 1)
	go func() {
		for {
			l, err := e.stdout.ReadString('\n')
			if l != "" {
				trimmed := strings.TrimRight(l, "\r\n")
				if trimmed == "<<END>>" {
					readDone <- nil
					return
				}
				select {
				case tokens <- trimmed:
				case <-ctx.Done():
					// Drain the remaining child output silently until
					// <<END>> so the stub/llamafile can finish cleanly
					// and be ready for the next request.
					for {
						l2, err2 := e.stdout.ReadString('\n')
						if l2 != "" && strings.TrimRight(l2, "\r\n") == "<<END>>" {
							readDone <- ctx.Err()
							return
						}
						if err2 != nil {
							readDone <- err2
							return
						}
					}
				}
			}
			if err != nil {
				readDone <- err
				return
			}
		}
	}()

	// Block until the reader goroutine finishes (either <<END>>, error,
	// or ctx cancellation with drain). This guarantees it is safe for
	// the caller to close the tokens channel immediately on return.
	return <-readDone
}

// Stop closes stdin (signalling the child to exit) and waits up to
// timeout for a clean exit. On timeout, Kill is called.
func (e *SubprocessEngine) Stop(timeout time.Duration) error {
	_ = e.stdin.Close()
	select {
	case <-e.done:
		return e.exit
	case <-time.After(timeout):
		_ = e.cmd.Process.Kill()
		<-e.done
		return fmt.Errorf("engine: stop timed out after %s, killed", timeout)
	}
}
