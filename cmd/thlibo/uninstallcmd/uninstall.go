// Package uninstallcmd implements `thlibo uninstall`: the clean
// reverse of `thlibo install`. Removes the PreToolUse hook entries
// from Claude Code's settings.json, deletes the hook scripts from
// ~/.thlibo/hooks/, and unregisters the daemon's autostart unit.
//
// Files deliberately left alone:
//
//   - ~/.thlibo/processors/ — user may have customised these
//   - ~/.thlibo/models/    — large downloads, deletion is explicit opt-in
//   - ~/.thlibo/logs/      — user may want to inspect audit history
//
// Pass --purge to also delete those three directories.
//
// See THREAT_MODEL.md finding #16 for why this command exists:
// persistence is the product, but a clean exit path is required.
package uninstallcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/3rg0n/thlibo/internal/adapters/claudecode"
	"github.com/3rg0n/thlibo/internal/install"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitUsage    = 2
	ExitDirError = 3
)

// Run drives one invocation. argv is everything after `thlibo uninstall`.
func Run(argv []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	var (
		dryRun        bool
		hookDir       string
		settingsPath  string
		purge         bool
		skipAutostart bool
	)
	fs.BoolVar(&dryRun, "dry-run", false, "report planned actions without applying them")
	fs.StringVar(&hookDir, "hook-dir", "", "override hook dir (default: ~/.thlibo/hooks)")
	fs.StringVar(&settingsPath, "settings", "", "override Claude Code settings path (default: ~/.claude/settings.json)")
	fs.BoolVar(&skipAutostart, "skip-autostart", false, "skip unregistering the daemon autostart")
	fs.BoolVar(&purge, "purge", false, "also delete ~/.thlibo (processors, models, logs)")
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: cannot determine home dir:", err)
		return ExitDirError
	}
	if hookDir == "" {
		hookDir = filepath.Join(home, ".thlibo", "hooks")
	}
	if settingsPath == "" {
		settingsPath = filepath.Join(home, ".claude", "settings.json")
	}
	bashHookPath := filepath.Join(hookDir, "thlibo-rewrite.sh")
	ps1HookPath := filepath.Join(hookDir, "thlibo-rewrite.ps1")
	readHookPath := filepath.Join(hookDir, "thlibo-read.sh")
	readPS1HookPath := filepath.Join(hookDir, "thlibo-read.ps1")
	writeHookPath := filepath.Join(hookDir, "thlibo-write.sh")
	writePS1HookPath := filepath.Join(hookDir, "thlibo-write.ps1")
	// ~/.claude/skills/caselog/ — installed by `thlibo install`.
	skillDir := filepath.Join(filepath.Dir(settingsPath), "skills", "caselog")

	fmt.Println("thlibo uninstall plan:")
	fmt.Println("  remove hook entries from:", settingsPath)
	fmt.Println("  delete hook scripts in:  ", hookDir)
	if purge {
		fmt.Println("  purge ~/.thlibo:          yes (processors, models, logs)")
	} else {
		fmt.Println("  purge ~/.thlibo:          no (use --purge to include)")
	}
	if dryRun {
		fmt.Println("  (dry-run: no changes applied)")
		return ExitOK
	}

	// 1. Remove hook entries from settings.json. Safe to call even
	// if the file doesn't exist - RemoveHooks returns nil in that
	// case.
	if err := claudecode.RemoveHooks(settingsPath); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: remove hooks from settings.json:", err)
		// Keep going; next steps still useful.
	} else {
		fmt.Println("  removed hook entries from settings.json")
	}

	// 2. Delete hook scripts (Exec + Read). Ignore os.IsNotExist -
	// missing file is the desired state anyway. Also removes the
	// ".new" conflict-preservation copies so uninstall leaves the
	// hooks directory clean.
	for _, p := range []string{
		bashHookPath, ps1HookPath,
		readHookPath, readPS1HookPath,
		writeHookPath, writePS1HookPath,
		bashHookPath + ".new", ps1HookPath + ".new",
		readHookPath + ".new", readPS1HookPath + ".new",
		writeHookPath + ".new", writePS1HookPath + ".new",
	} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "uninstall: delete hook script:", err)
		}
	}
	fmt.Println("  deleted hook scripts (Exec + Read + Write/Edit)")

	// 2b. Remove the /caselog skill directory. RemoveAll is a
	// no-op if the dir doesn't exist.
	if err := os.RemoveAll(skillDir); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall: remove /caselog skill:", err)
	} else {
		fmt.Println("  removed /caselog skill")
	}

	// 3. Unregister autostart. Platform-specific installer handles
	// the launchctl / systemctl / Startup-folder details.
	if !skipAutostart {
		a, err := install.NewInstaller()
		if err != nil {
			fmt.Fprintln(os.Stderr, "uninstall: autostart unsupported:", err)
		} else {
			name := os.Getenv("THLIBO_AUTOSTART_NAME")
			if name == "" {
				name = "cisco.thlibo.daemon"
			}
			if err := a.Uninstall(name); err != nil {
				fmt.Fprintln(os.Stderr, "uninstall: unregister autostart:", err)
			} else {
				fmt.Println("  unregistered autostart via", a.Mechanism())
			}
		}
	}

	// 4. Purge ~/.thlibo only if explicitly requested.
	if purge {
		thliboDir := filepath.Join(home, ".thlibo")
		if err := os.RemoveAll(thliboDir); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall: purge", thliboDir, ":", err)
		} else {
			fmt.Println("  purged", thliboDir)
		}
	}

	fmt.Println("thlibo uninstall complete.")
	return ExitOK
}
