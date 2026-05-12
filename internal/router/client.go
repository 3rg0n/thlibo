package router

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/3rg0n/thlibo/internal/ipc"
)

// DaemonClient is the middleware's view of thlibod. It's deliberately
// narrow: build a Request, open a socket, stream frames until done,
// return the concatenated tokens. Router and prompt dispatchers are
// its only callers.
type DaemonClient struct {
	// Address is the IPC address to dial. For Unix: /run/thlibo/infer.sock.
	// For Windows: \\.\pipe\thlibo-infer. For TCP fallback: 127.0.0.1:47320.
	Address string

	// UseTCP selects the TCP fallback transport; otherwise the native
	// Unix-socket / named-pipe dialer is used.
	UseTCP bool

	// Timeout bounds a single Post call end-to-end. A zero value means
	// no deadline (caller's ctx provides liveness).
	Timeout time.Duration
}

// Post sends req to the daemon and returns the concatenated token
// content plus the done-frame usage. A response containing any error
// frame returns that error verbatim. Context cancellation closes the
// connection and returns ctx.Err().
func (c *DaemonClient) Post(ctx context.Context, req ipc.Request) (string, *ipc.Usage, error) {
	conn, err := dialCtx(ctx, c)
	if err != nil {
		return "", nil, fmt.Errorf("router: dial daemon: %w", err)
	}
	defer conn.Close()

	if c.Timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(c.Timeout))
	}

	// Cancel the connection if the caller's ctx fires.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	wire, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("router: marshal request: %w", err)
	}
	wire = append(wire, '\n')
	if _, err := conn.Write(wire); err != nil {
		return "", nil, fmt.Errorf("router: write request: %w", err)
	}

	r := bufio.NewReader(conn)
	var content strings.Builder
	var usage *ipc.Usage
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// Under ctx cancellation this appears as a closed-conn error.
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
			return "", nil, fmt.Errorf("router: read frame: %w", err)
		}
		var resp ipc.Response
		if err := json.Unmarshal(line, &resp); err != nil {
			return "", nil, fmt.Errorf("router: decode frame: %w", err)
		}
		switch resp.Type {
		case ipc.ResponseToken:
			content.WriteString(resp.Content)
		case ipc.ResponseDone:
			usage = resp.Usage
			return content.String(), usage, nil
		case ipc.ResponseError:
			return "", nil, errors.New(resp.Message)
		case ipc.ResponseStatus:
			// Ignore startup/admin status frames on the inference socket.
		default:
			return "", nil, fmt.Errorf("router: unknown frame type %q", resp.Type)
		}
	}
}

func dialCtx(ctx context.Context, c *DaemonClient) (net.Conn, error) {
	var d net.Dialer
	if c.UseTCP {
		return d.DialContext(ctx, "tcp", c.Address)
	}
	return dialNative(ctx, c.Address)
}
