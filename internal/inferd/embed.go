package inferd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The embed surface is a *second*, separate protocol from generation.
// Generation is length-prefixed and type-tagged (protocol.go); embedding
// is line-delimited JSON on its own socket — one request line, one
// response line, per inferd ADR 0017. They share only the dialers, so
// they share only dial_unix.go / dial_windows.go.
//
// This exists because cordon-filter scores log windows by k-NN distance
// in embedding space (ADR 0008). Nothing else in thlibo embeds, which is
// why protocol.go's package doc said the embed socket was unimplemented.

// DefaultEmbedAddress returns inferd's embedding endpoint (inferd ADR
// 0017):
//
//	Linux/other   $XDG_RUNTIME_DIR/inferd/infer.embed.sock
//	              -> $HOME/.inferd/run/infer.embed.sock
//	              -> /tmp/inferd/infer.embed.sock
//	macOS         $TMPDIR/inferd/infer.embed.sock
//	Windows       \\.\pipe\inferd-infer-embed
//
// The Unix chain is runtimeDir(), the same resolution the generation
// socket uses, so a host where one socket resolves cannot have the other
// resolve somewhere else. (The retired Python cordon-filter stopped at
// $HOME and had no /tmp last resort; inheriting runtimeDir() adds that
// third step, which can only help — it is reached solely when both
// XDG_RUNTIME_DIR is unset and the home directory is unknown.)
func DefaultEmbedAddress() string {
	switch runtime.GOOS {
	case "windows":
		return `\\.\pipe\inferd-infer-embed`
	case "darwin":
		return filepath.Join(os.TempDir(), "inferd", "infer.embed.sock")
	default:
		return filepath.Join(runtimeDir(), "infer.embed.sock")
	}
}

// EmbedDimensions is the Matryoshka truncation length thlibo asks for.
// EmbeddingGemma 300M is MRL-trained, so a 256-dim prefix is a usable
// embedding on its own rather than a truncated 768-dim one.
const EmbedDimensions = 256

// EmbedTask is the task hint sent with each request. "clustering" is the
// right prompt for cordon's job (relative distance between windows),
// as opposed to the asymmetric query/document prompts used for retrieval.
const EmbedTask = "clustering"

// maxEmbedResponseBytes caps one NDJSON response line. A batch of 32
// windows at 256 float64s each is ~600 KB of JSON text; 16 MiB is far
// above that and far below the point where a malformed daemon could make
// bufio grow without bound. Independent of MaxFrameBytes because this is
// a different protocol with no length prefix to check up front.
const maxEmbedResponseBytes = 16 << 20

// EmbedClient dials inferd's embedding socket once per Embed call, the
// same passive-readiness posture as Client (ADR 0006): a connect failure
// is the daemon's "not ready" signal, surfaced as ErrBackendNotReady so
// the caller passes its input through untouched.
type EmbedClient struct {
	// Address is the embedding endpoint. Empty -> DefaultEmbedAddress.
	Address string

	// Timeout bounds one Embed round-trip. Zero resolves to
	// $THLIBO_INFERD_TIMEOUT, else DefaultTimeout; negative disables it.
	// Same knob as Client so an operator debugging a slow model does not
	// have to know there are two sockets.
	Timeout time.Duration

	// dialFunc, when non-nil, replaces the real dial. Test seam only.
	dialFunc func(ctx context.Context) (net.Conn, error)
}

func (c *EmbedClient) timeout() time.Duration {
	if c.Timeout < 0 {
		return 0
	}
	if c.Timeout > 0 {
		return c.Timeout
	}
	return resolveTimeout()
}

// embedRequest is one NDJSON request line (inferd ADR 0017).
type embedRequest struct {
	ID         string   `json:"id"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions"`
	Task       string   `json:"task"`
}

// embedResponse is one NDJSON response line. Type is "embeddings" on
// success and "error" on failure; unknown fields are ignored so a daemon
// that adds usage/backend detail does not break this client.
type embedResponse struct {
	Type       string      `json:"type"`
	ID         string      `json:"id"`
	Embeddings [][]float64 `json:"embeddings"`
	Dimensions int         `json:"dimensions"`
	Code       string      `json:"code"`
	Message    string      `json:"message"`
}

// Embed sends one batch of inputs and returns one vector per input, in
// order. It is the caller's job to batch: this writes a single request
// line and reads a single response line.
//
// A dial failure that looks like "daemon isn't up" wraps
// ErrBackendNotReady, matching Client.Post, so callers can distinguish
// "no inference available" (fail open, ADR 0006) from a protocol fault.
// A short or long vector list is an error rather than a silent partial
// result — cordon indexes the returned slice positionally against its
// windows, so a length mismatch would score the wrong text.
func (c *EmbedClient) Embed(ctx context.Context, inputs []string) (vecs [][]float64, err error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	addr := c.Address
	if addr == "" {
		addr = DefaultEmbedAddress()
	}

	// Bounded here, not at the call site, for the reason DefaultTimeout
	// documents: an unbounded wait on a wedged daemon is a hang, not a
	// fallback (ADR 0012). cordon runs inside a PreToolUse hook, so a
	// hang blocks the AI client outright.
	if d := c.timeout(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	// Same rewrite as Post: the watchdog below closes the connection on
	// expiry, so an in-flight read reports "closed pipe" rather than the
	// deadline. Report the context error so a blown deadline is
	// distinguishable from a wire fault. ErrBackendNotReady is exempt —
	// it is already the more actionable classification.
	defer func() {
		if err != nil && ctx.Err() != nil && !errors.Is(err, ErrBackendNotReady) {
			vecs, err = nil, ctx.Err()
		}
	}()

	dialer := c.dialFunc
	if dialer == nil {
		dialer = func(ctx context.Context) (net.Conn, error) {
			return dialNative(ctx, addr)
		}
	}
	conn, err := dialer(ctx)
	if err != nil {
		if isTransientConnect(err) {
			return nil, fmt.Errorf("%w: dial %s: %v", ErrBackendNotReady, addr, err)
		}
		return nil, fmt.Errorf("inferd: dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	body, err := json.Marshal(embedRequest{
		ID:         "thlibo",
		Input:      inputs,
		Dimensions: EmbedDimensions,
		Task:       EmbedTask,
	})
	if err != nil {
		return nil, fmt.Errorf("inferd: marshal embed request: %w", err)
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		// A write failure on a fresh connection is also a not-ready shape
		// (the daemon went away between dial and write).
		if isTransientConnect(err) || errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: write %s: %v", ErrBackendNotReady, addr, err)
		}
		return nil, fmt.Errorf("inferd: write embed request: %w", err)
	}

	line, err := readNDJSONLine(conn)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: %s closed without a response", ErrBackendNotReady, addr)
		}
		return nil, fmt.Errorf("inferd: read embed response: %w", err)
	}

	var resp embedResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("inferd: decode embed response: %w", err)
	}
	switch resp.Type {
	case "embeddings":
	case "error":
		return nil, fmt.Errorf("inferd: embed: %s: %s", resp.Code, resp.Message)
	default:
		return nil, fmt.Errorf("inferd: embed: unexpected response type %q", resp.Type)
	}
	if len(resp.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("inferd: embed returned %d vectors for %d inputs",
			len(resp.Embeddings), len(inputs))
	}
	for i, v := range resp.Embeddings {
		if len(v) == 0 {
			return nil, fmt.Errorf("inferd: embed vector %d is empty", i)
		}
	}
	return resp.Embeddings, nil
}

// readNDJSONLine reads one '\n'-terminated line, bounded by
// maxEmbedResponseBytes. bufio.Scanner is avoided deliberately: it
// reports a too-long line as a plain "token too long" with no way to
// distinguish it from EOF, and its buffer has to be pre-sized to the cap
// anyway. Reading bytes directly keeps the bound explicit and allocates
// only what arrives.
func readNDJSONLine(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(io.LimitReader(r, maxEmbedResponseBytes+1))
	line, err := br.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) > 0 {
			// A final line without its newline is still a usable frame.
			return line, nil
		}
		return nil, err
	}
	if len(line) > maxEmbedResponseBytes {
		return nil, fmt.Errorf("inferd: embed response exceeds %d byte cap", maxEmbedResponseBytes)
	}
	return line, nil
}

// EmbedUnavailable reports whether err means "no embedding backend right
// now" — the fail-open signal (ADR 0006) — as opposed to a bug on either
// side of the wire. Callers pass their input through untouched either
// way; this exists so they can log the two cases differently.
func EmbedUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrBackendNotReady) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "not ready")
}
