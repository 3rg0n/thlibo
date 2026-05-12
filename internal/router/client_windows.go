//go:build windows

package router

import (
	"context"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func dialNative(ctx context.Context, addr string) (net.Conn, error) {
	// winio.DialPipeContext handles ctx cancellation.
	dl, _ := ctx.Deadline()
	var timeout time.Duration
	if !dl.IsZero() {
		timeout = time.Until(dl)
		if timeout <= 0 {
			timeout = 1 * time.Millisecond
		}
	} else {
		timeout = 5 * time.Second
	}
	return winio.DialPipe(addr, &timeout)
}
