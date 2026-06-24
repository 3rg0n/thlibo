//go:build unix

package inferd

import (
	"context"
	"net"
)

// dialNative dials a Unix domain socket at the resolved generation
// address.
func dialNative(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}
