//go:build !darwin && !linux

package update

// sendToast is a no-op on platforms without a wired notification
// channel. macOS uses osascript; Linux uses notify-send. Windows
// toast support is deferred (needs an AUMID and a registered shortcut).
func sendToast() {}
