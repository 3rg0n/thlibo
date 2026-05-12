//go:build !windows

package router

import (
	"context"
	"net"
)

func dialNative(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}
