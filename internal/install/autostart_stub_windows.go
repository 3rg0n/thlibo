//go:build windows

package install

import "errors"

// On Windows, only newWindowsInstaller is implemented for real.
// The darwin/linux stubs exist so the switch in NewInstaller
// compiles; they're never reached at runtime.
func newDarwinInstaller() (Installer, error) { return nil, errors.New("unreachable") }
func newLinuxInstaller() (Installer, error)  { return nil, errors.New("unreachable") }
