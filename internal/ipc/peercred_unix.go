//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"syscall"
)

// peerIdentity (Unix) reads SO_PEERCRED. Falls back to a bare
// TCP-transport identity for loopback connections.
//
// macOS: SO_PEERCRED is Linux-specific; on darwin we currently
// return an empty identity with Transport="unix" so callers still
// get a plausible string form without guaranteeing kernel-enforced
// UID. Darwin-specific LOCAL_PEERCRED is a v0.3 item.
func peerIdentity(conn net.Conn) (PeerID, error) {
	switch c := conn.(type) {
	case *net.UnixConn:
		return unixPeer(c)
	case *net.TCPConn:
		// Loopback TCP — no kernel-enforced identity.
		return PeerID{Transport: "tcp", SID: conn.RemoteAddr().String()}, nil
	default:
		return PeerID{}, ErrNoPeerIdentity
	}
}

func unixPeer(c *net.UnixConn) (PeerID, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return PeerID{}, fmt.Errorf("ipc: peer syscall conn: %w", err)
	}
	var uid, gid, pid int
	var cerr error
	ctrl := raw.Control(func(fd uintptr) {
		ucred, err := getPeerUcred(int(fd))
		if err != nil {
			cerr = err
			return
		}
		uid = ucred.uid
		gid = ucred.gid
		pid = ucred.pid
	})
	if ctrl != nil {
		return PeerID{}, fmt.Errorf("ipc: peer control: %w", ctrl)
	}
	if cerr != nil {
		return PeerID{}, fmt.Errorf("ipc: peercred: %w", cerr)
	}
	return PeerID{UID: uid, GID: gid, PID: pid, Transport: "unix"}, nil
}

// peerUcred is the platform-agnostic shape the build-tagged helpers
// populate.
type peerUcred struct{ uid, gid, pid int }

// (Linux-only SO_PEERCRED implementation lives in peercred_linux.go.
// Darwin stub lives in peercred_darwin.go.)
var _ = syscall.SOCK_STREAM // silence unused-import on OSes without getsockopt linkage
