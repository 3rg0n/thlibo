package inferd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// ErrBackendNotReady is returned by Post when the inferd generation
// socket isn't bound (daemon down, restarting, draining, or still
// loading the model). Callers MUST treat this as the fail-open signal
// per ADR 0006 and pass the original bytes through.
var ErrBackendNotReady = errors.New("inferd: backend not ready")

// Client dials the inferd generation socket once per Post. Address is a
// Unix socket path (Unix) or a named-pipe path (Windows); empty resolves
// to the platform default via DefaultGenerationAddress. inferd binds no
// inbound network listener as of v0.5 (ADR 0022) — there is no TCP
// transport.
type Client struct {
	// Address is the inferd generation endpoint. Empty -> platform default.
	Address string

	// dialFunc, when non-nil, replaces the real dial. Test seam only;
	// production leaves it nil and dials Address.
	dialFunc func(ctx context.Context) (net.Conn, error)
}

// Post submits one generation request to inferd over the v0.4
// length-prefixed wire, streams the response to its terminal frame, and
// returns the collapsed result (concatenated text deltas + any
// structured tool_use blocks).
//
// On a connect failure that looks like "daemon not ready" the error
// wraps ErrBackendNotReady so the middleware fails open (ADR 0006).
func (c *Client) Post(ctx context.Context, req Request) (Result, error) {
	addr := c.Address
	if addr == "" {
		addr = DefaultGenerationAddress()
	}

	dialer := c.dialFunc
	if dialer == nil {
		dialer = func(ctx context.Context) (net.Conn, error) {
			return dialNative(ctx, addr)
		}
	}
	conn, err := dialer(ctx)
	if err != nil {
		if isTransientConnect(err) {
			return Result{}, fmt.Errorf("%w: dial %s: %v", ErrBackendNotReady, addr, err)
		}
		return Result{}, fmt.Errorf("inferd: dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	// Cancellation: closing the conn aborts the in-flight job (ADR 0007).
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	req.WireVersion = WireVersion
	body, err := json.Marshal(req)
	if err != nil {
		return Result{}, fmt.Errorf("inferd: marshal request: %w", err)
	}
	if err := writeFrame(conn, frameJSON, body); err != nil {
		// A write failure on a fresh connection is also a "not ready"
		// shape (daemon went away between dial and write).
		if isTransientConnect(err) || errors.Is(err, io.EOF) {
			return Result{}, fmt.Errorf("%w: write %s: %v", ErrBackendNotReady, addr, err)
		}
		return Result{}, fmt.Errorf("inferd: write request: %w", err)
	}

	// Attachments: per attachment in order, a JSON BlobDescriptor frame
	// then its raw 0x02 BLOB frame (protocol-v2.md §3.1/§3.7). The bytes
	// are raw decoded RGB (ADR 0016) — never base64.
	for _, att := range req.Attachments {
		desc, derr := json.Marshal(blobDescriptor{
			Type:         "attachment_blob",
			AttachmentID: att.ID,
			Len:          uint64(len(att.RGB)),
		})
		if derr != nil {
			return Result{}, fmt.Errorf("inferd: marshal blob descriptor: %w", derr)
		}
		if werr := writeFrame(conn, frameJSON, desc); werr != nil {
			if isTransientConnect(werr) || errors.Is(werr, io.EOF) {
				return Result{}, fmt.Errorf("%w: write blob desc: %v", ErrBackendNotReady, werr)
			}
			return Result{}, fmt.Errorf("inferd: write blob descriptor: %w", werr)
		}
		if werr := writeFrame(conn, frameBlob, att.RGB); werr != nil {
			if isTransientConnect(werr) || errors.Is(werr, io.EOF) {
				return Result{}, fmt.Errorf("%w: write blob: %v", ErrBackendNotReady, werr)
			}
			return Result{}, fmt.Errorf("inferd: write blob: %w", werr)
		}
	}

	return readResult(ctx, bufio.NewReader(conn), req.ID)
}

// readResult drains the response stream to its terminal frame and
// collapses it. Text deltas concatenate; tool_use blocks accumulate in
// order; thinking deltas are discarded. A terminal "error" frame
// becomes a Go error.
func readResult(ctx context.Context, r byteReader, id string) (Result, error) {
	var text strings.Builder
	var res Result

	for {
		ftype, payload, err := readFrame(r)
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				// Stream closed without a terminal frame. If we
				// accumulated text, treat it as the result for graceful
				// degradation; otherwise it's a "not ready" shape.
				if text.Len() > 0 || len(res.ToolCalls) > 0 {
					res.Text = text.String()
					return res, nil
				}
				return Result{}, fmt.Errorf("%w: stream closed without terminal frame", ErrBackendNotReady)
			}
			return Result{}, fmt.Errorf("inferd: read response: %w", err)
		}
		if ftype != frameJSON {
			// A BLOB frame on the response stream is a protocol error
			// (protocol-v2.md §4); the daemon only emits JSON responses.
			return Result{}, errors.New("inferd: daemon sent a non-JSON frame on the response stream")
		}

		var resp response
		if err := json.Unmarshal(payload, &resp); err != nil {
			return Result{}, fmt.Errorf("inferd: decode response: %w", err)
		}

		switch resp.Type {
		case "frame":
			if resp.Block == nil {
				continue
			}
			switch resp.Block.Type {
			case "text":
				text.WriteString(resp.Block.Delta)
			case "tool_use":
				res.ToolCalls = append(res.ToolCalls, ToolCall{
					ID:    resp.Block.ToolCallID,
					Name:  resp.Block.Name,
					Input: resp.Block.Input,
				})
			case "thinking":
				// Reasoning trace — not user-facing here; drop it.
			}
		case "done":
			res.Text = text.String()
			return res, nil
		case "error":
			return Result{}, fmt.Errorf("inferd: %s: %s", resp.Code, resp.Message)
		default:
			// Unknown terminal/non-terminal type: ignore (forward-compat).
		}
	}
}

// isTransientConnect reports whether err is a "server isn't ready yet"
// dial failure (→ passthrough) rather than a configuration bug
// (→ propagate). Matches the common refusal shapes for the UDS / named-
// pipe transports (inferd binds no network listener — ADR 0022).
func isTransientConnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, hint := range []string{
		"connection refused", // UDS with no listener bound (Linux/macOS)
		"no such file or directory",
		"cannot find the file",
		"all pipe instances are busy",
		"i/o timeout",
	} {
		if strings.Contains(s, hint) {
			return true
		}
	}
	return false
}
