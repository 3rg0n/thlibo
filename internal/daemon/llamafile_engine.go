package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// LlamafileEngine drives llamafile in HTTP server mode over a private
// Unix-domain socket. No TCP port is allocated or bound.
//
// llamafile supports Unix-socket binding natively via
//
//	--host /path/to/file.sock
//
// We generate a per-process socket path under the existing thlibo runtime
// directory ($TMPDIR/thlibo/llamafile-<pid>.sock) and build an HTTP client
// whose DialContext goes directly to that socket. All traffic between
// thlibod and llamafile stays in kernel memory with no network involvement.
//
// Stop sequences from the -stop flag are NOT passed as CLI arguments
// (llamafile --server mode does not accept --stop flags; they are
// per-request parameters). They travel in the "stop" field of each
// POST /v1/chat/completions request body.
//
// The Engine interface contract (Ready, Generate, Stop, Done, ExitErr) is
// identical to SubprocessEngine so the daemon lifecycle layer is unaware of
// which backend is running.
type LlamafileEngine struct {
	cmd        *exec.Cmd
	sockPath   string
	baseURL    string // "http://llamafile" — host is a dummy; transport uses sockPath
	stopTokens []string
	client     *http.Client // unix-socket client for all llamafile requests

	ready atomic.Bool
	done  chan struct{}
	exit  error
}

// StartLlamafileEngine spawns the llamafile binary at enginePath with
// extraArgs (e.g. -m model.gguf -c 32768) appended after the internal
// server flags. stopTokens is a list of per-request stop sequences.
//
// On macOS the binary is wrapped in /bin/sh because the APE polyglot
// format cannot be execve'd directly.
//
// The caller must call Stop when done; the engine is NOT ready until
// Ready() returns true (probed by the daemon's waitReady loop).
func StartLlamafileEngine(enginePath string, extraArgs, stopTokens []string) (*LlamafileEngine, error) {
	sockPath, err := engineSocketPath()
	if err != nil {
		return nil, fmt.Errorf("engine: pick socket path: %w", err)
	}
	_ = os.Remove(sockPath) // clean up any stale socket from a previous crash

	// Server flags prepended; extraArgs follow so operator overrides win.
	// --server puts llamafile in headless HTTP-only mode.
	// --host <path>.sock tells llamafile to bind on a Unix socket instead
	// of a TCP port (supported since llamafile 0.9 / llama.cpp b3xxx).
	serverArgs := []string{
		"--server",
		"--host", sockPath,
	}
	args := append(serverArgs, extraArgs...)

	var cmd *exec.Cmd
	// enginePath is operator-configured (flag or installer default),
	// never user-controllable at runtime — same contract as every
	// other exec.Command call in the repo. Both branches get their
	// own annotations; semgrep doesn't carry the suppression comment
	// across branches.
	if runtime.GOOS == "darwin" {
		// APE binary requires /bin/sh wrapper on macOS (ENOEXEC without it).
		shArgs := append([]string{enginePath}, args...)
		// #nosec G204 -- see above
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		cmd = exec.Command("/bin/sh", shArgs...)
	} else {
		// #nosec G204 -- see above
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		cmd = exec.Command(enginePath, args...)
	}

	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr // surface llamafile errors in thlibod's stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("engine: start: %w", err)
	}

	// Build an HTTP client whose transport dials the Unix socket.
	// The URL host ("llamafile") is a dummy label; no DNS lookup happens.
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
	}

	e := &LlamafileEngine{
		cmd:        cmd,
		sockPath:   sockPath,
		baseURL:    "http://llamafile",
		stopTokens: stopTokens,
		client:     client,
		done:       make(chan struct{}),
	}
	go e.watchExit()
	go e.pollHealth(100 * time.Millisecond)
	return e, nil
}

func (e *LlamafileEngine) watchExit() {
	e.exit = e.cmd.Wait()
	e.ready.Store(false)
	_ = os.Remove(e.sockPath)
	close(e.done)
}

// pollHealth probes GET /health on the given interval until the server
// reports healthy or the process exits.
func (e *LlamafileEngine) pollHealth(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-e.done:
			return
		case <-t.C:
			if e.checkHealth() {
				e.ready.Store(true)
				return
			}
		}
	}
}

func (e *LlamafileEngine) checkHealth() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var h struct {
		Status string `json:"status"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &h); err != nil {
		return false
	}
	return h.Status == "ok"
}

func (e *LlamafileEngine) Ready() bool          { return e.ready.Load() }
func (e *LlamafileEngine) Done() <-chan struct{} { return e.done }
func (e *LlamafileEngine) ExitErr() error       { return e.exit }

// Generate calls POST /v1/chat/completions (OpenAI-compatible SSE) over
// the Unix socket and streams content tokens into the tokens channel.
// Returns when the stream ends, ctx is cancelled, or an error occurs.
// The caller closes tokens after Generate returns.
func (e *LlamafileEngine) Generate(ctx context.Context, prompt GeneratePrompt, tokens chan<- string) error {
	if !e.ready.Load() {
		return ErrNotReady
	}

	var msgs []chatMessage
	if prompt.System != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: prompt.System})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: prompt.User})

	maxTokens := prompt.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	reqBody := chatCompletionRequest{
		Model:       "default",
		Messages:    msgs,
		Stream:      true,
		Temperature: prompt.Temperature,
		TopP:        prompt.TopP,
		TopK:        prompt.TopK,
		MaxTokens:   maxTokens,
		Stop:        e.stopTokens,
		Grammar:     prompt.Grammar,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("engine: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("engine: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Use a streaming client (no Timeout) so long generations don't hit a
	// deadline; ctx provides cancellation.
	streamClient := &http.Client{Transport: e.client.Transport}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("engine: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("engine: server error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			return nil
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			content := chunk.Choices[0].Delta.Content
			if content != "" {
				select {
				case tokens <- content:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("engine: stream read: %w", err)
	}
	return nil
}

// Stop signals the llamafile process to exit (SIGINT, then Kill after
// timeout) and waits for the done channel.
func (e *LlamafileEngine) Stop(timeout time.Duration) error {
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Signal(os.Interrupt)
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	select {
	case <-e.done:
		return e.exit
	case <-time.After(timeout):
		if e.cmd.Process != nil {
			_ = e.cmd.Process.Kill()
		}
		<-e.done
		return fmt.Errorf("engine: stop timed out after %s, killed", timeout)
	}
}

// engineSocketPath returns a unique socket path for this thlibod instance
// under the existing thlibo runtime directory.
func engineSocketPath() (string, error) {
	dir, err := thliboRuntimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("llamafile-%d.sock", os.Getpid())), nil
}

func thliboRuntimeDir() (string, error) {
	base := os.TempDir()
	dir := filepath.Join(base, "thlibo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// chatCompletionRequest is the OpenAI-compatible request body.
type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature"`
	TopP        float64       `json:"top_p"`
	TopK        int           `json:"top_k"`
	MaxTokens   int           `json:"max_tokens"`
	Stop        []string      `json:"stop,omitempty"`
	Grammar     string        `json:"grammar,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// sseChunk is the streaming delta chunk from /v1/chat/completions.
type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}
