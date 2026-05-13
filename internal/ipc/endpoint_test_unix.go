//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testAddress returns a path for a Unix domain socket that stays
// within macOS's sun_path limit (~104 chars including null). We
// can't use t.TempDir() on Darwin because the default
// /var/folders/.../Test*/001/... path from testing often exceeds
// that limit. Fall back to a short /tmp-based name per test run.
// The test itself is responsible for cleaning up via t.Cleanup when
// returning the listener — we don't pre-delete here because two
// parallel tests with the same `base` would otherwise race.
func testAddress(t *testing.T, base string) string {
	t.Helper()
	// Short base dir: /tmp on Linux/macOS. TempDir is still used
	// for file-based tests where the path length doesn't matter.
	dir := "/tmp"
	if _, err := os.Stat(dir); err != nil {
		dir = t.TempDir()
	}
	// Short unique name: first 6 chars of the test name + a
	// nanosecond stamp. Names like "TestListenAndAccept" are kept
	// short so the full path stays under 104 chars.
	short := base
	if len(short) > 8 {
		short = short[:8]
	}
	name := fmt.Sprintf("thl-%s-%d.sock", short, time.Now().UnixNano())
	p := filepath.Join(dir, name)
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

func dial(addr string) (net.Conn, error) {
	return net.Dial("unix", addr)
}
