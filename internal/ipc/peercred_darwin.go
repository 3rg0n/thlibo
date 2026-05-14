//go:build darwin

package ipc

import "syscall"

// getPeerUcred on Darwin: SO_PEERCRED doesn't exist; the rough
// equivalent is getpeereid() via LOCAL_PEERCRED which Go's syscall
// doesn't wrap directly. For v0.2 we return a zero-value ucred with
// Transport="unix" preserved by the caller so the caller still has
// a stable (if un-verified) identity string.
//
// Darwin-native LOCAL_PEERCRED enforcement is tracked as v0.3.
func getPeerUcred(fd int) (peerUcred, error) {
	_ = syscall.SOCK_STREAM
	return peerUcred{uid: -1, gid: -1, pid: 0}, nil
}
