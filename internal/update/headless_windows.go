//go:build windows

package update

import (
	"io"
	"os"

	"golang.org/x/term"
)

// isTTY reports whether f is a console handle on Windows. x/term
// wraps GetConsoleMode for us so the implementation matches the
// Unix file.
func isTTY(f io.Writer) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	// #nosec G115 — Windows HANDLEs fit in int per MSDN; this is the
	// canonical x/term invocation pattern.
	return term.IsTerminal(int(file.Fd()))
}
