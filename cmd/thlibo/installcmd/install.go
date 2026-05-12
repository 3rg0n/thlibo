// Package installcmd implements `thlibo install`.
//
// v0.1 scope (gate rows E3, E4):
//
//   - Mirror the embedded built-in processors to ~/.thlibo/processors/
//     so script processors have a real on-disk directory to chdir+exec
//     into.
//   - Write the Claude Code PreToolUse hook script somewhere stable.
//   - Merge the hook into ~/.claude/settings.json without clobbering
//     other hooks.
//
// Out of scope for v0.1: launchd/systemd/Windows Service registration
// (E1, E2) and model download (E5). Those land in follow-up commits
// once the v0.1 foreground daemon story is solid.
package installcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/3rg0n/thlibo/internal/adapters/claudecode"
	"github.com/3rg0n/thlibo/internal/install"
)

// Run executes `thlibo install`. Accepts:
//
//	--dry-run          Report what would be done; don't touch the
//	                   filesystem.
//	--processors-dir   Override ~/.thlibo/processors.
//	--hook-dir         Override ~/.thlibo/hooks.
//	--settings         Override ~/.claude/settings.json.
//	--skip-hook        Mirror processors only; don't touch settings.
//	--skip-autostart   Mirror + hook only; don't register autostart.
//	--daemon-path X    Path to thlibod for autostart (default: <this
//	                   binary's dir>/thlibod[.exe]).
func Run(argv []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	var (
		dryRun        bool
		processorsDir string
		hookDir       string
		settingsPath  string
		skipHook      bool
		skipAutostart bool
		daemonPath    string
	)
	fs.BoolVar(&dryRun, "dry-run", false, "report planned actions without applying them")
	fs.StringVar(&processorsDir, "processors-dir", "", "override processors dir (default: ~/.thlibo/processors)")
	fs.StringVar(&hookDir, "hook-dir", "", "override hook dir (default: ~/.thlibo/hooks)")
	fs.StringVar(&settingsPath, "settings", "", "override Claude Code settings path (default: ~/.claude/settings.json)")
	fs.BoolVar(&skipHook, "skip-hook", false, "skip installing the Claude Code hook")
	fs.BoolVar(&skipAutostart, "skip-autostart", false, "skip registering the daemon for autostart")
	fs.StringVar(&daemonPath, "daemon-path", "", "thlibod path for autostart (default: alongside this binary)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	if processorsDir == "" {
		processorsDir = install.DefaultProcessorsDir()
	}
	home, homeErr := os.UserHomeDir()
	if hookDir == "" {
		if homeErr != nil {
			fmt.Fprintln(os.Stderr, "install: cannot determine home dir:", homeErr)
			return 3
		}
		hookDir = filepath.Join(home, ".thlibo", "hooks")
	}
	if settingsPath == "" {
		if homeErr != nil {
			fmt.Fprintln(os.Stderr, "install: cannot determine home dir:", homeErr)
			return 3
		}
		settingsPath = filepath.Join(home, ".claude", "settings.json")
	}

	hookPath := filepath.Join(hookDir, "thlibo-rewrite.sh")

	if daemonPath == "" {
		daemonPath = defaultDaemonPath()
	}

	// Autostart installer is optional: on unsupported OSes we print
	// a manual-start hint instead of failing the whole install.
	var autostart install.Installer
	if !skipAutostart {
		a, err := install.NewInstaller()
		if err != nil {
			fmt.Fprintln(os.Stderr, "install: autostart unsupported:", err)
			fmt.Fprintln(os.Stderr, "install: continuing without autostart; run thlibod manually.")
			skipAutostart = true
		} else {
			autostart = a
		}
	}

	fmt.Println("thlibo install plan:")
	fmt.Println("  processors dir:", processorsDir)
	fmt.Println("  hook script:   ", hookPath)
	if !skipHook {
		fmt.Println("  settings file: ", settingsPath)
	} else {
		fmt.Println("  settings file:  (skipped)")
	}
	if !skipAutostart && autostart != nil {
		fmt.Printf("  autostart:      %s (daemon: %s)\n", autostart.Mechanism(), daemonPath)
	} else {
		fmt.Println("  autostart:      (skipped)")
	}
	if dryRun {
		fmt.Println("  (dry-run: no changes applied)")
		return 0
	}

	if err := install.MirrorBuiltins(processorsDir); err != nil {
		fmt.Fprintln(os.Stderr, "install: mirror processors:", err)
		return 4
	}
	fmt.Println("  mirrored built-in processors")

	if skipHook {
		return 0
	}

	if err := claudecode.WriteHookScript(hookPath); err != nil {
		fmt.Fprintln(os.Stderr, "install: write hook:", err)
		return 5
	}
	fmt.Println("  wrote hook script")

	if err := claudecode.MergeSettings(settingsPath, hookPath); err != nil {
		fmt.Fprintln(os.Stderr, "install: merge settings:", err)
		return 6
	}
	fmt.Println("  merged Claude Code settings.json")

	if !skipAutostart && autostart != nil {
		// Allow an override so CI and the .test/ sandbox can register
		// an autostart entry under an isolated name without touching
		// the user's real autostart list.
		name := os.Getenv("THLIBO_AUTOSTART_NAME")
		if name == "" {
			name = "cisco.thlibo.daemon"
		}
		spec := install.AutostartSpec{
			Name:       name,
			DaemonPath: daemonPath,
		}
		if err := autostart.Install(spec); err != nil {
			fmt.Fprintln(os.Stderr, "install: autostart:", err)
			return 7
		}
		fmt.Println("  registered autostart via", autostart.Mechanism())
	}

	fmt.Println("thlibo install complete.")
	return 0
}

// defaultDaemonPath picks the thlibod binary next to the running
// thlibo executable. This matches what the release bundle lays
// out: thlibo + thlibod side by side in <install-dir>/bin.
func defaultDaemonPath() string {
	self, err := os.Executable()
	if err != nil {
		return "thlibod"
	}
	dir := filepath.Dir(self)
	name := "thlibod"
	if runtimeIsWindows() {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// runtimeIsWindows wraps runtime.GOOS so the installcmd test file
// can override it (future cross-platform test harness). Currently
// just a thin wrapper; no tests need the seam yet.
func runtimeIsWindows() bool {
	return osPathSep == '\\'
}

const osPathSep = os.PathSeparator
