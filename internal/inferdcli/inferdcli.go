// Package inferdcli adapts the inferd Go client to thlibo's needs.
//
// Thlibo talks to inferd over the same NDJSON protocol-v1 the daemon
// publishes. Rather than have every middleware site dial inferd
// directly, we wrap the streaming inferd.Client.Generate API in a
// blocking Post(ctx, Request) -> (string, error) shape — that's what
// thlibo's prompt-processor dispatch path used to expect from the
// pre-v0.6 internal/router.DaemonClient, and keeping the same shape
// keeps the rewrite diff small at callsites.
//
// Two contracts this package codifies:
//
//   - **Passive readiness** (ADR 0006). Each Post does a fresh dial
//     against inferd's inference socket. Connect failure (ECONNREFUSED
//     on Unix, ERROR_FILE_NOT_FOUND on Windows) is the spec's
//     "inferd is not ready" signal — we surface it as ErrBackendNotReady
//     and the caller falls through to passthrough. We do NOT consult
//     the admin socket here; that's reserved for `thlibo doctor`.
//
//   - **Stream collapse**. inferd.Client.Generate streams Response
//     frames over a channel. For thlibo's compression path we want a
//     single string. Post drains the channel until the terminal
//     done/error frame, concatenates token contents, and returns the
//     whole thing.
//
// Path resolution lives in addr_*.go — the same XDG_RUNTIME_DIR vs
// $TMPDIR vs named-pipe split that thlibod's old endpoint code
// implemented, now scoped to inferd's path conventions.
package inferdcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	inferd "github.com/3rg0n/inferd/clients/go"
)

// ErrBackendNotReady is returned by Post when the inferd inference
// socket isn't bound (daemon down, restarting, draining, or still
// loading the model). Callers MUST treat this as the fail-open
// signal per ADR 0006 and pass the original bytes through.
var ErrBackendNotReady = errors.New("inferdcli: backend not ready")

// Client wraps a connection-per-call dialer at the configured inferd
// inference address. The address can be a Unix path, a Windows pipe
// path, or "host:port" for the loopback TCP fallback. An empty
// Address resolves to the platform default via DefaultInferenceAddress.
type Client struct {
	// Address is the inferd inference endpoint. Empty -> platform default.
	Address string

	// UseTCP routes the dial through TCP rather than the native
	// transport. Set true when Address is host:port.
	UseTCP bool
}

// Post submits one inference request to inferd, streams the response
// to terminal, and returns the concatenated content of every token
// frame plus the done/error metadata.
//
// The returned string is whatever the model produced — caller is
// responsible for processors.Strip() / TrimSpace as needed.
func (c *Client) Post(ctx context.Context, req inferd.Request) (string, error) {
	addr := c.Address
	if addr == "" {
		addr = DefaultInferenceAddress()
	}

	conn, err := dial(ctx, addr, c.UseTCP)
	if err != nil {
		if isTransientConnect(err) {
			return "", fmt.Errorf("%w: dial %s: %v", ErrBackendNotReady, addr, err)
		}
		return "", fmt.Errorf("inferdcli: dial %s: %w", addr, err)
	}
	cl := inferd.New(conn)
	defer cl.Close()

	stream, err := cl.Generate(ctx, req)
	if err != nil {
		return "", fmt.Errorf("inferdcli: generate: %w", err)
	}

	var b strings.Builder
	for resp := range stream {
		switch resp.Type {
		case inferd.ResponseToken:
			b.WriteString(resp.Content)
		case inferd.ResponseDone:
			// Some processors return the whole answer in the done
			// frame's Content rather than as token chunks. Append it
			// only if non-empty so streamed-and-complete responses
			// don't double up.
			if resp.Content != "" && b.Len() == 0 {
				b.WriteString(resp.Content)
			}
			return b.String(), nil
		case inferd.ResponseError:
			return "", fmt.Errorf("inferdcli: %s: %s", resp.Code, resp.Message)
		case inferd.ResponseStatus:
			// Status frames are informational; ignore.
		}
	}
	// Channel closed without a terminal frame — usually a transport
	// error already surfaced as a synthetic error frame. Treat the
	// accumulated text as the result for graceful degradation.
	if b.Len() > 0 {
		return b.String(), nil
	}
	return "", errors.New("inferdcli: response stream closed without terminal frame")
}

// dial connects to the inferd inference endpoint using the appropriate
// transport. Unix path -> "unix"; Windows pipe path -> os.OpenFile via
// the inferd client's pipe shim; host:port -> "tcp".
func dial(ctx context.Context, addr string, forceTCP bool) (net.Conn, error) {
	if forceTCP || looksLikeTCP(addr) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
	return dialNative(ctx, addr)
}

// looksLikeTCP detects a "host:port" shape: contains a colon AND every
// segment after the last colon is digits. The Unix path
// "/run/user/1000/inferd/infer.sock" contains no colons; the Windows
// pipe path "\\.\pipe\inferd-infer" contains only the path-prefix
// colon, no port.
func looksLikeTCP(addr string) bool {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return false
	}
	port := addr[i+1:]
	if port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isTransientConnect reports whether err is the kind of dial failure
// that means "the server isn't ready yet" rather than "the
// configuration is wrong." Transient errors trigger passthrough;
// non-transient errors propagate as configuration bugs.
func isTransientConnect(err error) bool {
	if err == nil {
		return false
	}
	// io.EOF mid-stream is technically transient too, but Post catches
	// that elsewhere; this function only sees errors from net.Dial.
	if errors.Is(err, io.EOF) {
		return true
	}
	// Most real-world cases land here: ECONNREFUSED on Unix when no
	// listener is bound, ERROR_FILE_NOT_FOUND on Windows for a missing
	// named pipe, and ERROR_PIPE_BUSY when inferd is mid-restart.
	s := err.Error()
	for _, hint := range []string{
		"connection refused",   // Linux / macOS
		"actively refused it",  // Windows connectex
		"no such file or directory",
		"cannot find the file",
		"all pipe instances are busy",
		"connect: timeout",
		"i/o timeout",
		"network is unreachable",
	} {
		if strings.Contains(strings.ToLower(s), hint) {
			return true
		}
	}
	return false
}
