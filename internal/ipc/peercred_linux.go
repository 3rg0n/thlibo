//go:build linux

package ipc

import "syscall"

// getPeerUcred reads SO_PEERCRED for the Unix-domain socket fd.
// Linux only; darwin implements a stub at peercred_darwin.go.
func getPeerUcred(fd int) (peerUcred, error) {
	u, err := syscall.GetsockoptUcred(fd, syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return peerUcred{}, err
	}
	return peerUcred{uid: int(u.Uid), gid: int(u.Gid), pid: int(u.Pid)}, nil
}
