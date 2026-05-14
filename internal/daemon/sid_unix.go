//go:build !windows

package daemon

// daemonSIDMatches is Windows-only; on Unix we never reach the
// "windows" transport branch of peerAllowed, so this stub exists
// solely to satisfy cross-platform compilation. Returning false
// here would be a false positive; returning true is safe because
// peerAllowed only calls this on the "windows" branch.
func daemonSIDMatches(_ string) (bool, error) { return true, nil }
