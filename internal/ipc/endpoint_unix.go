//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// listenNative creates a Unix domain socket at cfg.Address with the
// permissions and group membership required by spec §IPC endpoints.
//
// Sequence (matters for security):
//  1. Remove any stale socket file from a previous daemon that didn't shut
//     down cleanly.
//  2. Umask to 0077 so the socket is created with no world/group bits,
//     then restore umask. We chmod explicitly afterwards.
//  3. net.Listen creates the socket file.
//  4. chown to daemon uid + requested group (if Group is set and we can
//     resolve it). Failure to resolve the group is a warning, not fatal;
//     the daemon can still serve the local user.
//  5. chmod to the kind's mode (0660 inference, 0600 admin).
func listenNative(cfg EndpointConfig) (net.Listener, error) {
	_ = os.Remove(cfg.Address)

	oldMask := syscall.Umask(0o077)
	l, err := net.Listen("unix", cfg.Address)
	syscall.Umask(oldMask)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen unix %s: %w", cfg.Address, err)
	}

	mode := os.FileMode(modeFor(cfg.Kind))
	if err := os.Chmod(cfg.Address, mode); err != nil {
		_ = l.Close()
		_ = os.Remove(cfg.Address)
		return nil, fmt.Errorf("ipc: chmod %s: %w", cfg.Address, err)
	}

	if cfg.Group != "" {
		if err := chownToGroup(cfg.Address, cfg.Group); err != nil {
			_ = l.Close()
			_ = os.Remove(cfg.Address)
			return nil, fmt.Errorf("ipc: chown %s to group %s: %w", cfg.Address, cfg.Group, err)
		}
	}
	return l, nil
}

func chownToGroup(path, groupName string) error {
	g, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("lookup group %q: %w", groupName, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("parse gid %q: %w", g.Gid, err)
	}
	// -1 uid keeps the current owner.
	return os.Chown(path, -1, gid)
}
