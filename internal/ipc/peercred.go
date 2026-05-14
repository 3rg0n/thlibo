package ipc

import (
	"errors"
	"net"
	"strconv"
)

// PeerID identifies the process on the other end of an IPC
// connection. Populated by PeerIdentity; the exact fields available
// depend on the transport.
//
// See THREAT_MODEL.md finding #24.
type PeerID struct {
	// UID is the peer's effective user ID on Unix, or -1 when
	// unavailable.
	UID int
	// GID is the peer's effective group ID on Unix, or -1 when
	// unavailable.
	GID int
	// PID is the peer's process ID on Unix, or the named-pipe
	// client PID on Windows. Zero when unavailable.
	PID int
	// SID is the peer's user SID string on Windows; on the TCP
	// transport it carries the remote address text so String()
	// can produce a stable identifier. Empty on Unix.
	SID string
	// Transport names the source of this identity so operators
	// can tell "kernel-enforced" from "self-reported".
	Transport string
}

// ErrNoPeerIdentity is returned when the underlying transport does
// not support peer-credential extraction.
var ErrNoPeerIdentity = errors.New("ipc: peer identity unavailable on this transport")

// PeerIdentity returns the best-effort identity of the process on
// the other end of conn. Implemented per-platform:
//   - Unix: SO_PEERCRED (Linux) / LOCAL_PEERCRED (macOS).
//   - Windows: GetNamedPipeClientProcessId + OpenProcessToken.
//   - TCP loopback: returns Transport="tcp" with the remote address.
func PeerIdentity(conn net.Conn) (PeerID, error) {
	return peerIdentity(conn)
}

// String returns a stable string form suitable for use as
// queue.Job.CallerID. Format:
//
//	unix:<uid>:<pid>
//	windows:<sid>:<pid>
//	tcp:<remote-addr>
//
// An empty PeerID (Transport=="") returns the empty string, which
// the queue treats as "skip per-caller accounting".
func (p PeerID) String() string {
	switch p.Transport {
	case "unix":
		return "unix:" + strconv.Itoa(p.UID) + ":" + strconv.Itoa(p.PID)
	case "windows":
		return "windows:" + p.SID + ":" + strconv.Itoa(p.PID)
	case "tcp":
		return "tcp:" + p.SID
	default:
		return ""
	}
}
