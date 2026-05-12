//go:build darwin

package install

import "errors"

func newWindowsInstaller() (Installer, error) { return nil, errors.New("unreachable") }
func newLinuxInstaller() (Installer, error)   { return nil, errors.New("unreachable") }
