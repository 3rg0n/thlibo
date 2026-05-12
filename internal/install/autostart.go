// Package install's autostart support registers thlibod to launch at
// user logon without requiring admin/root, password prompts, or
// system-wide service registration. Each OS has its own user-scoped
// mechanism:
//
//	Windows: a .cmd shim in the Startup folder
//	         (%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup)
//	macOS:   a LaunchAgent plist in ~/Library/LaunchAgents/
//	         loaded via launchctl bootstrap + bootout
//	Linux:   a user systemd unit in ~/.config/systemd/user/
//	         enabled via systemctl --user
//
// All three run thlibod as the current user, no elevation. Exactly
// matches what the v0.1 decision says: "install to the startup app
// list on Windows and whatever is similar on mac/Linux."
package install

import (
	"fmt"
	"runtime"
)

// AutostartSpec is the information every platform backend needs to
// lay down a functional auto-launch entry.
type AutostartSpec struct {
	// Name is the stable identifier the OS sees. For Windows it's
	// the .cmd filename; for macOS it's the plist label; for Linux
	// it's the unit name. Keep it dns-safe: `cisco.thlibo.daemon`.
	Name string

	// DaemonPath is the absolute path to thlibod(.exe).
	DaemonPath string

	// Args are extra flags passed to thlibod on launch.
	Args []string

	// WorkingDir is where the daemon is chdir'd to before launch.
	// Can be empty (the OS mechanism will decide a default).
	WorkingDir string

	// LogPath is where stdout + stderr are redirected. Empty means
	// the OS default (typically discarded on macOS launchd, user
	// journal on systemd, swallowed on Windows).
	LogPath string
}

// Installer is the interface each platform backend implements.
type Installer interface {
	// Install creates or updates the autostart entry. Idempotent.
	Install(spec AutostartSpec) error

	// Uninstall removes the autostart entry. No-op if not installed.
	Uninstall(name string) error

	// Status reports whether an entry named name is currently
	// registered for autostart.
	Status(name string) (installed bool, err error)

	// Mechanism returns a short human-readable name of the
	// underlying OS mechanism, e.g. "Startup folder" on Windows.
	Mechanism() string
}

// NewInstaller returns the autostart installer for the current OS.
// Unsupported platforms return an error so the installer prints a
// clear "manual-start required" message rather than silently
// claiming success.
func NewInstaller() (Installer, error) {
	switch runtime.GOOS {
	case "windows":
		return newWindowsInstaller()
	case "darwin":
		return newDarwinInstaller()
	case "linux":
		return newLinuxInstaller()
	default:
		return nil, fmt.Errorf("install: autostart not supported on %s (run thlibod manually)", runtime.GOOS)
	}
}
