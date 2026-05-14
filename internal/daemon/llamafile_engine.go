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
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// LlamafileEngine drives llamafile in HTTP server mode.
//
// On creation it allocates a random loopback port, spawns llamafile with
// --port/--host/--nobrowser, and begins polling GET /health in the
// background. Ready() flips true once the server accepts the first healthy
// request. Generate() calls POST /v1/chat/completions with SSE streaming.
//
// The Engine interface contract (Ready, Generate, Stop, Done, ExitErr) is
// identical to SubprocessEngine so the daemon lifecycle layer is unaware of
// which backend is running.
type LlamafileEngine struct {
	cmd     *exec.Cmd
	port    int
	baseURL string
	client  *http.Client // short-timeout client for health probes

	ready atomic.Bool
	done  chan struct{}
	exit  error
}

// StartLlamafileEngine spawns the llamafile binary at enginePath with
// extraArgs (e.g. -m model.gguf -c 32768 --stop token) appended after
// the internal server flags. On macOS the binary is wrapped in /bin/sh
// because the APE polyglot format cannot be execve'd directly.
//
// The caller must call Stop when done; the engine is NOT ready until
// Ready() returns true (probed by the daemon's waitReady loop).
func StartLlamafileEngine(enginePath string, extraArgs []string) (*LlamafileEngine, error) {
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("engine: find free port: %w", err)
	}

	// Server flags prepended; extraArgs follow so operator overrides win.
	serverArgs := []string{
		"--port", fmt.Sprintf("%d", port),
		"--host", "127.0.0.1",
		"--nobrowser",
	}
	args := append(serverArgs, extraArgs...)

	var cmd *exec.Cmd
	// #nosec G204 — enginePath is operator-configured (flag/installer default),
	// never user-controllable at runtime. nosemgrep: dangerous-exec-command
	if runtime.GOOS == "darwin" {
		// APE binary requires /bin/sh wrapper on macOS (ENOEXEC without it).
		shArgs := append([]string{enginePath}, args...)
		// #nosec G204
		cmd = exec.Command("/bin/sh", shArgs...)
	} else {
		cmd = exec.Command(enginePath, args...)
	}

	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr // surface llamafile errors in thlibod's stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("engine: start: %w", err)
	}

	e := &LlamafileEngine{
		cmd:     cmd,
		port:    port,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		client:  &http.Client{Timeout: 2 * time.Second},
		done:    make(chan struct{}),
	}
	go e.watchExit()
	go e.pollHealth(100 * time.Millisecond)
	return e, nil
}

func (e *LlamafileEngine) watchExit() {
	e.exit = e.cmd.Wait()
	e.ready.Store(false)
	close(e.done)
}

// pollHealth probes GET /health on the given interval until the server
// reports healthy or the process exits. It sets ready atomically so the
// daemon's waitReady loop sees it via Ready().
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
	resp, err := e.client.Get(e.baseURL + "/health") //nolint:noctx
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 503 = model loading; anything else = not ready
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

func (e *LlamafileEngine) Ready() bool              { return e.ready.Load() }
func (e *LlamafileEngine) Done() <-chan struct{}     { return e.done }
func (e *LlamafileEngine) ExitErr() error           { return e.exit }

// Generate calls POST /v1/chat/completions (OpenAI-compatible SSE) and
// streams content tokens into the tokens channel. Returns when the stream
// ends, ctx is cancelled, or an error occurs. The caller closes tokens
// after Generate returns.
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

	// No timeout on the streaming client — ctx provides cancellation.
	resp, err := http.DefaultClient.Do(httpReq)
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

// freePort finds an available loopback TCP port by briefly binding :0
// and recording the assigned port. There is a small TOCTOU window before
// llamafile binds; on loopback this is acceptable for a local daemon.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
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
