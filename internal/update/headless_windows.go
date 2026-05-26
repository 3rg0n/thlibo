//go:build windows

package update

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func isTTY(f io.Writer) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	var mode uint32
	err := windows.GetConsoleMode(windows.Handle(file.Fd()), &mode)
	return err == nil
}
