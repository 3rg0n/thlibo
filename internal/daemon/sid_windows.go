//go:build windows

package daemon

import (
	"sync"

	"golang.org/x/sys/windows"
)

var (
	cachedDaemonSID     string
	cachedDaemonSIDErr  error
	cachedDaemonSIDOnce sync.Once
)

// daemonSIDMatches reports whether peerSID equals the daemon
// process's own user SID. The SID is cached on first lookup so
// each incoming connection doesn't pay the token-open cost.
func daemonSIDMatches(peerSID string) (bool, error) {
	cachedDaemonSIDOnce.Do(func() {
		tok := windows.GetCurrentProcessToken()
		defer tok.Close()
		user, err := tok.GetTokenUser()
		if err != nil {
			cachedDaemonSIDErr = err
			return
		}
		cachedDaemonSID = user.User.Sid.String()
	})
	if cachedDaemonSIDErr != nil {
		return false, cachedDaemonSIDErr
	}
	return peerSID == cachedDaemonSID, nil
}
