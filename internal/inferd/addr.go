Looking at the issue, the problem is in the `addr.go` file for macOS socket resolution. The issue mentions in Step 4 that:

> On macOS the path will be under `$TMPDIR` or `$XDG_RUNTIME_DIR` per `protocol-v2.md` §1.1 — please paste what you actually get, since macOS socket resolution is the least-exercised branch of `internal/inferd/addr.go`.

The current macOS implementation only uses `os.TempDir()` (which maps to `$TMPDIR` on macOS), but doesn't follow the same fallback chain as Linux or consider `$XDG_RUNTIME_DIR`. The fix should make macOS use `$XDG_RUNTIME_DIR` first (when available), then fall back to `$TMPDIR`.

Looking at the protocol-v2.md §1.1 reference and the Linux fallback chain, macOS should also respect `XDG_RUNTIME_DIR` when set, and use `$TMPDIR/inferd/inferd.sock` as the default (since macOS doesn't have logind but may have XDG vars set).

```go
package inferd

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultGenerationAddress returns the inferd generation endpoint
// thlibo dials by default (protocol-v2.md §1, ADR 0021). v0.4 unified
// generation onto a single neutral socket:
//
//	Linux/other   $XDG_RUNTIME_DIR/inferd/inferd.sock
//	              -> $HOME/.inferd/run/inferd.sock
//	              -> /tmp/inferd/inferd.sock
//	macOS         $XDG_RUNTIME_DIR/inferd/inferd.sock  (if set)
//	              -> $TMPDIR/inferd/inferd.sock
//	Windows       \\.\pipe\inferd
//
// (The pre-v0.4 paths inferd-infer / infer.sock are removed; a daemon
// no longer binds them.)
func DefaultGenerationAddress() string {
	switch runtime.GOOS {
	case "windows":
		return `\\.\pipe\inferd`
	case "darwin":
		return filepath.Join(darwinRuntimeDir(), "inferd.sock")
	default: // linux and other unix
		return filepath.Join(runtimeDir(), "inferd.sock")
	}
}

// DefaultAdminAddress returns inferd's admin socket. Not used by the
// hot dispatch path (ADR 0006) — reserved for diagnostics. Admin keeps
// its name across versions (protocol-v2.md §1).
func DefaultAdminAddress() string {
	switch runtime.GOOS {
	case "windows":
		return `\\.\pipe\inferd-admin`
	case "darwin":
		return filepath.Join(darwinRuntimeDir(), "admin.sock")
	default:
		return filepath.Join(runtimeDir(), "admin.sock")
	}
}

// darwinRuntimeDir resolves the per-user runtime directory for inferd's
// Unix sockets on macOS (protocol-v2.md §1.1):
//  1. $XDG_RUNTIME_DIR/inferd (if explicitly set, e.g. in CI or dev environments)
//  2. $TMPDIR/inferd (macOS default; os.TempDir() returns $TMPDIR on darwin)
func darwinRuntimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "inferd")
	}
	return filepath.Join(os.TempDir(), "inferd")
}

// runtimeDir resolves the per-user runtime directory for inferd's Unix
// sockets, first hit wins (protocol-v2.md §1.1):
//  1. $XDG_RUNTIME_DIR/inferd (systemd-logind sessions)
//  2. $HOME/.inferd/run (sessions without logind)
//  3. /tmp/inferd (last resort)
func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "inferd")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".inferd", "run")
	}
	return filepath.Join(os.TempDir(), "inferd")
}
```