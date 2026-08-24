package codex

import "runtime"

// runtimeIsWindows is a seam so tests can force either host, for the
// same reason the claudecode adapter has one (#127): which script the
// installer registers is host-dependent, so the Windows behaviour needs
// a test that runs on Linux CI and the Unix behaviour one that runs on
// Windows.
var runtimeIsWindows = func() bool { return runtime.GOOS == "windows" }

// HookFileName returns the hook script filename for this host: the
// PowerShell variant on Windows, the bash variant elsewhere.
//
// Never register the bash script on Windows. A bare `.sh` path in a hook
// `command` is resolved through the .sh file association, which on a
// Git-for-Windows box is git-bash.exe — a GUI terminal launcher, not an
// interpreter. The hook opens a window and never feeds the tool, and
// fail-open hides it (#127, same defect class, Claude Code adapter).
// The mirror case is real too: `powershell -ExecutionPolicy` is
// Windows-only, so the `.ps1` must never be registered elsewhere.
func HookFileName() string {
	if runtimeIsWindows() {
		return hookMarkerPS1
	}
	return hookMarkerSh
}
