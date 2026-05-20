//go:build unix

package inferdcli

import (
	"context"
	"net"
)

// dialNative dials a Unix domain socket. Path comes from the inferd
// inference address resolution.
func dialNative(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}
