//go:build !windows

package update

import (
	"io"
	"os"

	"golang.org/x/term"
)

// isTTY reports whether f is a terminal. Uses x/term so the same
// implementation works across Linux (TCGETS), macOS/BSD (TIOCGETA),
// and other Unix flavors without per-OS build tags.
func isTTY(f io.Writer) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
