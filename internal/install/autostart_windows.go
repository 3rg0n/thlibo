//go:build windows

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type windowsInstaller struct {
	startupDir string
}

func newWindowsInstaller() (Installer, error) {
	// The Startup folder per current user. APPDATA is always set
	// on login; we honour it in case the user has a roaming profile
	// that moves it. Everything we write lives under APPDATA, which
	// is the correct scope for a per-user autostart.
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("install: cannot find APPDATA: %w", err)
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	dir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	return &windowsInstaller{startupDir: dir}, nil
}

func (w *windowsInstaller) Mechanism() string { return "Startup folder" }

// Install drops a .cmd shim in the Startup folder that launches
// thlibod without a console window (via `start` with /B). Using a
// .cmd rather than a .lnk keeps the installer dependency-free
// (shortcuts require the COM IShellLink interface).
func (w *windowsInstaller) Install(spec AutostartSpec) error {
	if err := os.MkdirAll(w.startupDir, 0o750); err != nil {
		return fmt.Errorf("install: create startup dir: %w", err)
	}
	path := filepath.Join(w.startupDir, spec.Name+".cmd")
	content := w.cmdBody(spec)
	// Windows ignores POSIX mode bits; Go writes with default ACLs.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("install: write startup cmd: %w", err)
	}
	return nil
}

func (w *windowsInstaller) Uninstall(name string) error {
	path := filepath.Join(w.startupDir, name+".cmd")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (w *windowsInstaller) Status(name string) (bool, error) {
	path := filepath.Join(w.startupDir, name+".cmd")
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

// cmdBody returns the body of the .cmd file. It uses `start "" /B`
// so thlibod runs detached without a console window flashing on
// logon. Arguments are joined with double-quoting around paths
// that contain spaces.
func (w *windowsInstaller) cmdBody(spec AutostartSpec) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("rem thlibo autostart — installed by `thlibo install`. Remove to disable.\r\n")
	if spec.WorkingDir != "" {
		fmt.Fprintf(&b, "cd /D %s\r\n", quoteIfNeeded(spec.WorkingDir))
	}
	b.WriteString(`start "" /B `)
	b.WriteString(quoteIfNeeded(spec.DaemonPath))
	for _, a := range spec.Args {
		b.WriteString(" ")
		b.WriteString(quoteIfNeeded(a))
	}
	if spec.LogPath != "" {
		// cmd's redirection binds to `start`, not the child. We
		// prefer discarding to the null device over complicating
		// the invocation; users wanting logs can run thlibod
		// manually with -v instead.
		_ = spec.LogPath
	}
	b.WriteString("\r\n")
	return b.String()
}

// quoteIfNeeded returns s wrapped in double quotes when it contains
// whitespace or shell metacharacters; otherwise s is returned
// unchanged. cmd.exe doesn't need escape for backslashes so we
// don't touch those.
func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ` &|<>^"`) {
		// Escape embedded quotes per cmd.exe rules: "" inside a
		// quoted string is one literal quote.
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}
