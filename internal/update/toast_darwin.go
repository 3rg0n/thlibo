//go:build darwin

package update

import (
	"os/exec"
)

// toastTitle and toastBody are compile-time constants — no
// release-server content interpolated (prompt-injection guard).
const toastTitle = "thlibo"
const toastBody = "New update available — run: thlibo upgrade"

// sendToast fires a macOS Notification Center toast via osascript.
// Best-effort: if osascript is absent (sandbox, TCC denial, headless
// CI) the error is silently discarded — the stderr banner is the
// primary notice channel.
func sendToast() {
	script := `display notification "` + toastBody + `" with title "` + toastTitle + `"`
	// #nosec G204 — script is a compile-time constant, no user input
	cmd := exec.Command("osascript", "-e", script)
	_ = cmd.Run()
}
