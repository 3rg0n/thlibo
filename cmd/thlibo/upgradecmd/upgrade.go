// Package upgradecmd implements `thlibo upgrade`.
//
// The upgrade subcommand shells out to the signed install script via
// `bash -c "curl … | bash"`, which handles binary replacement,
// re-registration of hooks, and the macOS quarantine strip. thlibo
// deliberately does not download and self-replace its own binary —
// that path is handled by the same installer users ran initially,
// keeping the upgrade surface identical to the install surface.
package upgradecmd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

const installScriptURL = "https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh"

// Run executes `thlibo upgrade [--version v0.X.Y]`.
// Exit codes follow BSD sysexits: 0 ok, 64 usage, 1 failure.
func Run(argv []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: thlibo upgrade [--version <tag>]

Download and install the latest thlibo release (or a pinned version)
using the same signed installer script as the initial install.

Options:
  --version v0.X.Y   Install a specific tagged release instead of
                     the latest. Equivalent to setting
                     THLIBO_VERSION=<tag> before running the script.
`)
	}
	var version string
	fs.StringVar(&version, "version", "", "pin to a specific release tag")
	if err := fs.Parse(argv); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 64
	}

	// THLIBO_VERSION in the environment takes precedence over the
	// flag (existing installers rely on it); the flag is a
	// convenience wrapper.
	if version != "" {
		if err := os.Setenv("THLIBO_VERSION", version); err != nil {
			fmt.Fprintln(os.Stderr, "upgrade: setenv:", err)
			return 1
		}
	}

	switch runtime.GOOS {
	case "windows":
		return runWindows()
	default:
		return runUnix()
	}
}

// runUnix pipes the install script through bash.
func runUnix() int {
	script := fmt.Sprintf("curl -fsSL %s | bash", installScriptURL)
	// #nosec G204 — script is a compile-time constant URL
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- script is fmt.Sprintf'd from the const installScriptURL only; no caller-controlled input reaches the shell.
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "upgrade: install script failed:", err)
		return 1
	}
	return 0
}

// runWindows pipes the install script through PowerShell.
func runWindows() int {
	psURL := "https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.ps1"
	script := fmt.Sprintf(
		`$ErrorActionPreference = 'Stop'; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12; Invoke-Expression (Invoke-WebRequest -Uri '%s' -UseBasicParsing).Content`,
		psURL,
	)
	// #nosec G204 — script is a compile-time constant URL
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- script is fmt.Sprintf'd from the const psURL only; no caller-controlled input reaches PowerShell.
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "upgrade: install script failed:", err)
		return 1
	}
	return 0
}
