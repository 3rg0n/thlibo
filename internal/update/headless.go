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

// headlessAutoSignals are environment variables whose mere presence
// (any non-empty value) marks the process as running inside a
// non-interactive agent or CI runner. Listed individually so each
// signal is grep-able.
//
// Why these: thlibo's whole upgrade path leans on hooks invoked by
// AI agents (Claude Code, Codex) — not the user's shell. If we only
// looked at CI=true + isTTY, the headless notice would never fire
// for the most common deployment shape (an agent piping tool output
// through `thlibo`).
var headlessAutoSignals = []string{
	// Generic CI marker — set by GitHub Actions, GitLab CI, CircleCI,
	// Travis, AppVeyor, Buildkite, Drone, etc. Listed first because
	// most CI providers honor it.
	"CI",
	// Per-provider markers (fallbacks for runners that don't set CI).
	"GITHUB_ACTIONS",
	"GITLAB_CI",
	"BUILDKITE",
	"CIRCLECI",
	"JENKINS_URL",
	// Agent markers — these are the load-bearing signals for thlibo's
	// primary use case: AI assistants invoking it via tool hooks.
	"CLAUDECODE",
	"CLAUDE_CODE_SESSION_ID",
	"CODEX",
	"CODEX_SESSION_ID",
}

// IsHeadless reports whether the current process is running in a
// non-interactive / headless environment. Resolution order:
//
//  1. THLIBO_HEADLESS=1   → headless (explicit override)
//  2. THLIBO_HEADLESS=0   → interactive (explicit override; beats
//                           every auto-signal below)
//  3. Any of headlessAutoSignals set to a non-empty value → headless
//  4. CI=true             → headless (already covered by #3, kept
//                           in the doc for clarity)
//  5. stderr not a TTY    → headless (piped / redirected)
//  6. Otherwise           → interactive
func IsHeadless() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("THLIBO_HEADLESS"))) {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	}
	for _, key := range headlessAutoSignals {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return !isTTY(os.Stderr)
}
