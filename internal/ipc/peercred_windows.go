//go:build windows

package ipc

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Lazy-load GetNamedPipeClientProcessId from kernel32. It's not
// exposed in golang.org/x/sys/windows as of the version we pin.
var (
	modKernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procGetNamedPipeClientProcessId = modKernel32.NewProc("GetNamedPipeClientProcessId")
)

// peerIdentity (Windows) reads the client PID via
// GetNamedPipeClientProcessId, opens the client process, reads its
// access token, and extracts the user SID.
//
// TCP loopback returns Transport="tcp" with the remote address; no
// kernel-enforced identity available on that path.
func peerIdentity(conn net.Conn) (PeerID, error) {
	if tc, ok := conn.(*net.TCPConn); ok {
		return PeerID{Transport: "tcp", SID: tc.RemoteAddr().String()}, nil
	}

	pid, err := namedPipeClientPID(conn)
	if err != nil {
		return PeerID{}, err
	}

	hProc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return PeerID{}, fmt.Errorf("ipc: open peer process %d: %w", pid, err)
	}
	defer windows.CloseHandle(hProc)

	var hToken windows.Token
	if err := windows.OpenProcessToken(hProc, windows.TOKEN_QUERY, &hToken); err != nil {
		return PeerID{}, fmt.Errorf("ipc: open peer token: %w", err)
	}
	defer hToken.Close()

	user, err := hToken.GetTokenUser()
	if err != nil {
		return PeerID{}, fmt.Errorf("ipc: token user: %w", err)
	}
	sid := user.User.Sid.String()
	return PeerID{PID: int(pid), SID: sid, Transport: "windows"}, nil
}

// namedPipeClientPID extracts the client-side process ID. Uses the
// Handle()-returning interface that go-winio's PipeConn implements.
func namedPipeClientPID(conn net.Conn) (uint32, error) {
	type handleProvider interface {
		Handle() windows.Handle
	}
	hp, ok := conn.(handleProvider)
	if !ok {
		return 0, fmt.Errorf("ipc: connection does not expose a pipe handle (type %T)", conn)
	}
	var pid uint32
	// #nosec G103 -- unsafe.Pointer is the idiomatic way to pass a
	// DWORD* out-parameter to a Win32 DLL call via
	// LazyProc.Call(uintptr...). pid is a stack-local uint32; no
	// aliasing or lifetime issue.
	r1, _, err := procGetNamedPipeClientProcessId.Call(
		uintptr(hp.Handle()),
		uintptr(unsafe.Pointer(&pid)),
	)
	if r1 == 0 {
		if err == nil || err == syscall.Errno(0) {
			err = fmt.Errorf("unknown error")
		}
		return 0, fmt.Errorf("ipc: GetNamedPipeClientProcessId: %w", err)
	}
	return pid, nil
}
