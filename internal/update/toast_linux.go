//go:build linux

package update

import (
	"context"
	"os/exec"
	"time"
)

// toastTitle and toastBody are compile-time constants — no
// release-server content interpolated (prompt-injection guard).
const toastTitle = "thlibo"
const toastBody = "New update available — run: thlibo upgrade"

// sendToast fires a libnotify desktop toast via notify-send.
// Best-effort: if notify-send is absent (server, minimal container,
// no DBus session) the error is silently discarded — the stderr
// banner is the primary notice channel.
//
// 2-second timeout guards against a stuck DBus session: notify-send
// blocks until it gets a reply from the notification daemon.
func sendToast() {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// #nosec G204 — title/body are compile-time constants, no user input
	cmd := exec.CommandContext(ctx,
		"notify-send",
		"--app-name=thlibo",
		"--icon=dialog-information",
		toastTitle,
		toastBody,
	)
	_ = cmd.Run()
}
