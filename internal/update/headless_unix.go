//go:build !windows

package update

import (
	"io"
	"os"
	"golang.org/x/sys/unix"
)

func isTTY(f io.Writer) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	return err == nil
}
