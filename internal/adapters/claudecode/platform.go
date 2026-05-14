package claudecode

import "runtime"

// runtimeIsWindows is a tiny seam so tests can shadow this via
// monkey-patching if ever needed. Direct call-through today; kept
// in a separate file so future platform-specific logic lands here
// rather than cluttering claudecode.go.
func runtimeIsWindows() bool { return runtime.GOOS == "windows" }
