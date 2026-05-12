//go:build !windows

package ipc

import (
	"net"
	"path/filepath"
	"testing"
)

func testAddress(t *testing.T, base string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), base+".sock")
}

func dial(addr string) (net.Conn, error) {
	return net.Dial("unix", addr)
}
