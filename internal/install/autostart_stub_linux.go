//go:build linux

package install

import "errors"

func newWindowsInstaller() (Installer, error) { return nil, errors.New("unreachable") }
func newDarwinInstaller() (Installer, error)  { return nil, errors.New("unreachable") }
