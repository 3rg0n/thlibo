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
func Run(argv []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	var (
		dryRun        bool
		processorsDir string
		hookDir       string
		settingsPath  string
		skipHook      bool
	)
	fs.BoolVar(&dryRun, "dry-run", false, "report planned actions without applying them")
	fs.StringVar(&processorsDir, "processors-dir", "", "override processors dir (default: ~/.thlibo/processors)")
	fs.StringVar(&hookDir, "hook-dir", "", "override hook dir (default: ~/.thlibo/hooks)")
	fs.StringVar(&settingsPath, "settings", "", "override Claude Code settings path (default: ~/.claude/settings.json)")
	fs.BoolVar(&skipHook, "skip-hook", false, "skip installing the Claude Code hook")
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

	fmt.Println("thlibo install plan:")
	fmt.Println("  processors dir:", processorsDir)
	fmt.Println("  hook script:   ", hookPath)
	if !skipHook {
		fmt.Println("  settings file: ", settingsPath)
	} else {
		fmt.Println("  settings file:  (skipped)")
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

	fmt.Println("thlibo install complete.")
	return 0
}
