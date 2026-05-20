// Default endpoint resolution for inferd's inference socket on each
// platform. Mirrors the inferd protocol-v1 spec's recommended paths
// (vendored in docs/inferd-admin-protocol-v1.md) and falls back
// through XDG_RUNTIME_DIR -> $HOME/.inferd/run -> $TMPDIR/inferd in
// the same order inferd's daemon resolves on Linux.

package inferdcli

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultInferenceAddress returns the inferd inference endpoint
// thlibo dials by default. Override at the Client level by setting
// Client.Address.
func DefaultInferenceAddress() string {
	switch runtime.GOOS {
	case "linux":
		return filepath.Join(linuxRuntimeDir(), "infer.sock")
	case "darwin":
		return filepath.Join(os.TempDir(), "inferd", "infer.sock")
	case "windows":
		return `\\.\pipe\inferd-infer`
	default:
		return filepath.Join(linuxRuntimeDir(), "infer.sock")
	}
}

// DefaultAdminAddress returns inferd's admin socket. Not used by the
// hot dispatch path (ADR 0006) — reserved for `thlibo doctor` and the
// /caselog skill, where progress UX matters.
func DefaultAdminAddress() string {
	switch runtime.GOOS {
	case "linux":
		return filepath.Join(linuxRuntimeDir(), "admin.sock")
	case "darwin":
		return filepath.Join(os.TempDir(), "inferd", "admin.sock")
	case "windows":
		return `\\.\pipe\inferd-admin`
	default:
		return filepath.Join(linuxRuntimeDir(), "admin.sock")
	}
}

// linuxRuntimeDir picks the per-user runtime directory for inferd's
// sockets on Linux. Same algorithm thlibod's old code used, scoped
// to inferd's name. systemd's --user services automatically have
// XDG_RUNTIME_DIR set; non-logind sessions and containers fall back
// to $HOME/.inferd/run.
func linuxRuntimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "inferd")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".inferd", "run")
	}
	return filepath.Join(os.TempDir(), "inferd")
}
