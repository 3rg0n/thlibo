package update

import (
	"os"
	"strings"
)

// NoticeLine is the exact bytes prepended to tool stdout in headless
// mode when an update is available. Compile-time constant: no release
// content (tag names, URLs) is interpolated, so a compromised release
// server cannot inject arbitrary text into Claude's context window.
const NoticeLine = "[thlibo] new update available, run: thlibo upgrade\n"

// IsHeadless reports whether the current process is running in a
// non-interactive / headless environment. Resolution order:
//
//  1. THLIBO_HEADLESS=1  → headless (explicit override)
//  2. THLIBO_HEADLESS=0  → interactive (explicit override, beats CI=true)
//  3. CI=true            → headless (standard CI signal)
//  4. stderr not a TTY   → headless (piped / redirected)
//  5. Otherwise          → interactive
func IsHeadless() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("THLIBO_HEADLESS"))) {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("CI"))) == "true" {
		return true
	}
	return !isTTY(os.Stderr)
}
