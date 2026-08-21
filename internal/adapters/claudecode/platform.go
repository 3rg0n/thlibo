package claudecode

import "runtime"

// runtimeIsWindows is a seam so tests can force either host. It has to
// be a var, not a func: which script MergeSettingsAll registers is now
// host-dependent (#127), so the Windows behaviour needs a test that
// runs on Linux CI and the Unix behaviour one that runs on Windows.
// Kept in a separate file so future platform-specific logic lands here
// rather than cluttering claudecode.go.
var runtimeIsWindows = func() bool { return runtime.GOOS == "windows" }
