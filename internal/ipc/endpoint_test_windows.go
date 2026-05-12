//go:build windows

package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

func testAddress(t *testing.T, base string) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return `\\.\pipe\` + base + "-" + hex.EncodeToString(b[:])
}

func dial(addr string) (net.Conn, error) {
	t := 2 * time.Second
	return winio.DialPipe(addr, &t)
}
