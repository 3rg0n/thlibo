//go:build !darwin

package update

// sendToast is a no-op on non-Darwin platforms. Desktop notification
// support for Linux (libnotify) and Windows (PowerShell toast) can be
// added if needed; for now only macOS is wired.
func sendToast() {}
