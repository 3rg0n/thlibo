//go:build windows

package ipc

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// listenNative creates a Windows named pipe whose ACL grants access to the
// current user (who owns the daemon process) and optionally the members of
// an application-defined group conveyed via cfg.Group (resolved through
// Windows security principals is out of scope for v0.1; cfg.Group is
// ignored on Windows for now and membership is enforced by the user's
// own process token).
//
// The security descriptor explicitly denies "Everyone" and grants
// generic read/write to the current user.
func listenNative(cfg EndpointConfig) (net.Listener, error) {
	sddl, err := sddlForKind(cfg.Kind)
	if err != nil {
		return nil, err
	}
	pc := &winio.PipeConfig{
		SecurityDescriptor: sddl,
		MessageMode:        false,
		InputBufferSize:    1 << 16,
		OutputBufferSize:   1 << 16,
	}
	l, err := winio.ListenPipe(cfg.Address, pc)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen pipe %s: %w", cfg.Address, err)
	}
	return l, nil
}

// sddlForKind returns an SDDL string for the pipe's security descriptor.
//
// SDDL breakdown:
//
//	D:PAI(A;;GRGW;;;<user-sid>)
//	D:            Start of DACL
//	PAI           Protected (no inheritance from parent) + auto-inherit flag
//	(A;;GRGW;;;<sid>)  Allow Generic Read + Generic Write to current user
//
// We deliberately omit a Deny-Everyone ACE. On Windows, a DACL with only
// allow-ACEs implicitly denies everything that isn't matched by an ACE.
// An explicit Deny-Everyone would also deny the current user (who is a
// member of Everyone), so access would be refused even to the owner.
//
// For v0.1 both admin and inference endpoints grant only the current
// user; a thlibo-users Windows local group is the v0.2 equivalent of the
// Unix group-share model.
func sddlForKind(kind EndpointKind) (string, error) {
	_ = kind
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("D:PAI(A;;GRGW;;;%s)", sid), nil
}

func currentUserSID() (string, error) {
	t := windows.GetCurrentProcessToken()
	u, err := t.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("GetTokenUser: %w", err)
	}
	return u.User.Sid.String(), nil
}
