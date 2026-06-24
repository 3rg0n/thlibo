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
//	macOS         $TMPDIR/inferd/inferd.sock
//	Windows       \\.\pipe\inferd
//
// (The pre-v0.4 paths inferd-infer / infer.sock are removed; a daemon
// no longer binds them.)
func DefaultGenerationAddress() string {
	switch runtime.GOOS {
	case "windows":
		return `\\.\pipe\inferd`
	case "darwin":
		return filepath.Join(os.TempDir(), "inferd", "inferd.sock")
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
		return filepath.Join(os.TempDir(), "inferd", "admin.sock")
	default:
		return filepath.Join(runtimeDir(), "admin.sock")
	}
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
