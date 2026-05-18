package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// EndpointKind identifies whether an endpoint serves inference or admin.
// The kind drives permissions: inference is group-shared (thlibo-users),
// admin is daemon-uid-only.
type EndpointKind int

const (
	EndpointInference EndpointKind = iota
	EndpointAdmin
)

func (k EndpointKind) String() string {
	switch k {
	case EndpointInference:
		return "inference"
	case EndpointAdmin:
		return "admin"
	default:
		return "unknown"
	}
}

// EndpointConfig captures what the daemon needs to create one IPC listener.
// Address format varies by platform:
//   - Unix: filesystem path (e.g. /run/thlibo/infer.sock)
//   - Windows: named pipe path (e.g. \\.\pipe\thlibo-infer)
//   - TCP fallback: host:port (e.g. 127.0.0.1:47320)
//
// Group is only meaningful on Unix; ignored on Windows and TCP.
type EndpointConfig struct {
	Kind    EndpointKind
	Address string
	Group   string // Unix only: group name (e.g. "thlibo-users")
	UseTCP  bool   // Fallback mode: bind a TCP listener at Address
}

// DefaultInferenceAddress returns the spec's per-platform default path
// for the inference endpoint.
func DefaultInferenceAddress() string {
	switch runtime.GOOS {
	case "linux":
		return filepath.Join(linuxRuntimeDir(), "infer.sock")
	case "darwin":
		return filepath.Join(os.TempDir(), "thlibo", "infer.sock")
	case "windows":
		return `\\.\pipe\thlibo-infer`
	default:
		return filepath.Join(linuxRuntimeDir(), "infer.sock")
	}
}

// DefaultAdminAddress returns the spec's per-platform default path for
// the admin endpoint.
func DefaultAdminAddress() string {
	switch runtime.GOOS {
	case "linux":
		return filepath.Join(linuxRuntimeDir(), "admin.sock")
	case "darwin":
		return filepath.Join(os.TempDir(), "thlibo", "admin.sock")
	case "windows":
		return `\\.\pipe\thlibo-admin`
	default:
		return filepath.Join(linuxRuntimeDir(), "admin.sock")
	}
}

// linuxRuntimeDir picks the per-user runtime directory for thlibo's
// sockets and lock file on Linux. /run/thlibo is unwritable to a
// non-root systemd --user service, so we use $XDG_RUNTIME_DIR (which
// systemd-logind already provisions per session at /run/user/<uid>)
// and fall back to $HOME/.thlibo/run when the env var is missing
// (non-graphical sessions, containers without logind, etc.). The
// daemon lifecycle creates this dir before binding the listener.
func linuxRuntimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "thlibo")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".thlibo", "run")
	}
	return filepath.Join(os.TempDir(), "thlibo")
}

// DefaultTCPFallbackAddress is the spec's loopback-only fallback when
// Unix sockets / named pipes are unavailable.
const DefaultTCPFallbackAddress = "127.0.0.1:47320"

// Listen creates the IPC listener described by cfg. On Unix, it creates a
// Unix domain socket with the correct group and mode (0660 for inference,
// 0600 for admin). On Windows, it creates a named pipe whose ACL denies
// Everyone and grants the current user. With UseTCP, it binds a TCP
// listener on cfg.Address (loopback only is the caller's responsibility).
//
// Listen does NOT create the containing directory on Unix; the daemon
// lifecycle creates /run/thlibo/ (or equivalent) once, before the first
// Listen call.
func Listen(cfg EndpointConfig) (net.Listener, error) {
	if cfg.UseTCP {
		return listenTCP(cfg.Address)
	}
	return listenNative(cfg)
}

func listenTCP(addr string) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ipc: tcp listen %s: %w", addr, err)
	}
	return l, nil
}

// modeFor returns the Unix file mode appropriate for an endpoint kind.
func modeFor(kind EndpointKind) uint32 {
	if kind == EndpointAdmin {
		return 0o600
	}
	return 0o660
}
