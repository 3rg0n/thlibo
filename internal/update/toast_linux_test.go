//go:build linux

package update

import (
	"testing"
	"time"
)

// TestSendToastLinuxBoundedDuration verifies sendToast returns quickly
// even when DBus has no Notifications service (the typical WSL /
// container shape). The 2 s context timeout is a hard ceiling; real
// notify-send invocations on a stuck session bus shouldn't pin the
// update goroutine forever.
func TestSendToastLinuxBoundedDuration(t *testing.T) {
	t0 := time.Now()
	sendToast()
	elapsed := time.Since(t0)
	if elapsed > 3*time.Second {
		t.Errorf("sendToast took %s; must be bounded by the 2s timeout", elapsed)
	}
}
