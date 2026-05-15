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
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/3rg0n/thlibo/internal/adapters/claudecode"
	"github.com/3rg0n/thlibo/internal/adapters/codex"
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
	var enginePath string
	fs.StringVar(&enginePath, "engine-path", "", "llamafile/engine path passed to thlibod -engine (default: next to thlibod)")
	var pullModel bool
	var pullEngine bool
	var allowUnpinned bool
	fs.BoolVar(&pullModel, "pull-model", false, "download the default GGUF as part of install (~5 GB)")
	fs.BoolVar(&pullEngine, "pull-engine", false, "download the llamafile engine binary as part of install (~838 MB)")
	fs.BoolVar(&allowUnpinned, "allow-unpinned", false, "allow --pull-model/--pull-engine to download without a pinned SHA (bootstrap only)")
	var installCodex bool
	var codexHooksPath string
	fs.BoolVar(&installCodex, "codex", false, "also install the Codex CLI hook (advisory until Codex lands updatedInput support)")
	fs.StringVar(&codexHooksPath, "codex-hooks", "", "override Codex hooks.json path (default: ~/.codex/hooks.json)")
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
	// Second hook for Claude Code's PowerShell tool
	// (CLAUDE_CODE_USE_POWERSHELL_TOOL=1). We install both unconditionally
	// - the user's Claude Code runtime will only invoke the matcher it
	// actually uses, so an unused hook just sits on disk at ~32 KB.
	ps1HookPath := filepath.Join(hookDir, "thlibo-rewrite.ps1")
	// Read-tool hooks: Claude fires `Read` when the user drops a file
	// into the window or types "read <path>". The hook script runs
	// `thlibo case` on large log-shaped files and rewrites
	// tool_input.file_path to the compressed variant so Claude sees
	// a small version instead of the raw blob.
	readHookPath := filepath.Join(hookDir, "thlibo-read.sh")
	readPS1HookPath := filepath.Join(hookDir, "thlibo-read.ps1")
	// Write+Edit-tool hooks: when auto_shorthand_on_write is enabled
	// in ~/.thlibo/config.yaml AND the target path matches the
	// configured glob list, intercept the Write/Edit envelope and
	// run the content through `thlibo shorthand` before the file
	// hits disk. Off by default — installs the script regardless,
	// but the runtime decision is config-gated. See
	// internal/config/config.go for the schema.
	writeHookPath := filepath.Join(hookDir, "thlibo-write.sh")
	writePS1HookPath := filepath.Join(hookDir, "thlibo-write.ps1")

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
	if installCodex {
		cp := codexHooksPath
		if cp == "" && home != "" {
			cp = filepath.Join(home, ".codex", "hooks.json")
		}
		fmt.Printf("  codex hooks:    %s\n", cp)
	} else {
		fmt.Println("  codex hooks:    (skipped; use --codex to install)")
	}
	if pullEngine {
		fmt.Printf("  engine:         llamafile v%s -> %s\n",
			install.DefaultEngine.Version, install.EngineDir())
	} else {
		fmt.Println("  engine:         (not downloaded — thlibod will fail without it)")
		fmt.Println("                  run `thlibo install --pull-engine` to download (~838 MB)")
	}
	if pullModel {
		fmt.Printf("  model:          %s -> %s\n",
			install.DefaultModel.Name, install.ModelsDir())
	} else {
		fmt.Println("  model:          (not downloaded; run `thlibo pull` separately)")
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
		return runDownloads(pullEngine, pullModel, allowUnpinned)
	}

	shResult, err := claudecode.WriteHookScript(hookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: write hook:", err)
		return 5
	}
	switch shResult {
	case claudecode.WriteResultConflict:
		fmt.Printf("  Bash hook: your edits preserved — new version written to %s.new\n", hookPath)
		fmt.Println("             review and merge manually, then remove the .new file.")
	default:
		fmt.Printf("  Bash hook script: %s\n", shResult)
	}

	ps1Result, err := claudecode.WriteHookScriptPS1(ps1HookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: write ps1 hook:", err)
		return 5
	}
	switch ps1Result {
	case claudecode.WriteResultConflict:
		fmt.Printf("  PowerShell hook: your edits preserved — new version written to %s.new\n", ps1HookPath)
		fmt.Println("                   review and merge manually, then remove the .new file.")
	default:
		fmt.Printf("  PowerShell hook script: %s\n", ps1Result)
	}

	// Read-tool hooks. Same conflict semantics as the Exec hooks.
	readResult, err := claudecode.WriteHookReadScript(readHookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: write read hook:", err)
		return 5
	}
	switch readResult {
	case claudecode.WriteResultConflict:
		fmt.Printf("  Read hook (bash): your edits preserved — new version written to %s.new\n", readHookPath)
	default:
		fmt.Printf("  Read hook (bash): %s\n", readResult)
	}

	readPS1Result, err := claudecode.WriteHookReadScriptPS1(readPS1HookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: write read ps1 hook:", err)
		return 5
	}
	switch readPS1Result {
	case claudecode.WriteResultConflict:
		fmt.Printf("  Read hook (ps1):  your edits preserved — new version written to %s.new\n", readPS1HookPath)
	default:
		fmt.Printf("  Read hook (ps1):  %s\n", readPS1Result)
	}

	// Write+Edit hooks. Installed but config-gated at runtime: a
	// fresh install never auto-rewrites the user's files until they
	// flip auto_shorthand_on_write to true in ~/.thlibo/config.yaml.
	writeResult, err := claudecode.WriteHookWriteScript(writeHookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: write write hook:", err)
		return 5
	}
	switch writeResult {
	case claudecode.WriteResultConflict:
		fmt.Printf("  Write hook (bash): your edits preserved — new version written to %s.new\n", writeHookPath)
	default:
		fmt.Printf("  Write hook (bash): %s\n", writeResult)
	}

	writePS1Result, err := claudecode.WriteHookWriteScriptPS1(writePS1HookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: write write ps1 hook:", err)
		return 5
	}
	switch writePS1Result {
	case claudecode.WriteResultConflict:
		fmt.Printf("  Write hook (ps1):  your edits preserved — new version written to %s.new\n", writePS1HookPath)
	default:
		fmt.Printf("  Write hook (ps1):  %s\n", writePS1Result)
	}

	if err := claudecode.MergeSettingsAll(settingsPath, claudecode.MergeHooks{
		BashExecHook:  hookPath,
		PS1ExecHook:   ps1HookPath,
		BashReadHook:  readHookPath,
		PS1ReadHook:   readPS1HookPath,
		BashWriteHook: writeHookPath,
		PS1WriteHook:  writePS1HookPath,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "install: merge settings:", err)
		return 6
	}
	fmt.Println("  merged Claude Code settings.json (Bash + PowerShell + Read + Write/Edit matchers)")
	fmt.Println("  Write/Edit auto-shorthand is OFF by default; enable in ~/.thlibo/config.yaml")

	// Mirror the /caselog skill into ~/.claude/skills/caselog/.
	// Uses the same SHA-stamp / conflict semantics as the hooks so
	// user edits survive reinstalls.
	skillsDir := filepath.Join(filepath.Dir(settingsPath), "skills")
	skillResult, err := claudecode.InstallCaselogSkill(skillsDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: write /caselog skill:", err)
		return 6
	}
	switch skillResult {
	case claudecode.WriteResultConflict:
		target := filepath.Join(skillsDir, "caselog", "SKILL.md")
		fmt.Printf("  /caselog skill: your edits preserved — new version at %s.new\n", target)
	default:
		fmt.Printf("  /caselog skill: %s\n", skillResult)
	}

	if !skipAutostart && autostart != nil {
		// Allow an override so CI and the .test/ sandbox can register
		// an autostart entry under an isolated name without touching
		// the user's real autostart list.
		name := os.Getenv("THLIBO_AUTOSTART_NAME")
		if name == "" {
			name = "cisco.thlibo.daemon"
		}
		// Always pass explicit -engine and -model so the daemon doesn't
		// have to resolve paths from HOME at runtime. LaunchAgents on
		// macOS run with a stripped environment where HOME may be unset.
		// See issue #11.
		resolvedEngine := enginePath
		if resolvedEngine == "" {
			resolvedEngine = filepath.Join(install.EngineDir(), install.EngineName())
		}
		resolvedModel := filepath.Join(install.ModelsDir(), install.DefaultModel.Filename)
		var args []string
		args = append(args, "-engine", resolvedEngine, "-model", resolvedModel)
		spec := install.AutostartSpec{
			Name:       name,
			DaemonPath: daemonPath,
			Args:       args,
		}
		if err := autostart.Install(spec); err != nil {
			fmt.Fprintln(os.Stderr, "install: autostart:", err)
			return 7
		}
		fmt.Println("  registered autostart via", autostart.Mechanism())
	}

	if installCodex {
		cp := codexHooksPath
		if cp == "" {
			if homeErr != nil {
				fmt.Fprintln(os.Stderr, "install: cannot determine home dir for Codex:", homeErr)
				return 3
			}
			cp = filepath.Join(home, ".codex", "hooks.json")
		}
		cfgPath := filepath.Join(filepath.Dir(cp), "config.toml")
		codexHookPath := filepath.Join(hookDir, "thlibo-rewrite-codex.sh")
		if err := codex.WriteHookScript(codexHookPath); err != nil {
			fmt.Fprintln(os.Stderr, "install: codex hook:", err)
			return 9
		}
		if err := codex.MergeHooksJSON(cp, codexHookPath); err != nil {
			fmt.Fprintln(os.Stderr, "install: codex hooks.json:", err)
			return 9
		}
		if err := codex.EnableHooksFeatureFlag(cfgPath); err != nil {
			fmt.Fprintln(os.Stderr, "install: codex config.toml:", err)
			return 9
		}
		fmt.Printf("  wrote Codex hook + merged %s + enabled codex_hooks in %s\n", cp, cfgPath)
	}

	return runDownloads(pullEngine, pullModel, allowUnpinned)
}

// runDownloads handles the optional --pull-engine and --pull-model
// steps. Called both from the normal path and from the --skip-hook
// early-return path so download flags are always honoured.
func runDownloads(pullEngine, pullModel, allowUnpinned bool) int {
	if pullEngine {
		fmt.Println("  downloading engine (this may take a while)...")
		engineDest, err := install.PullEngine(contextCancellableOnSignal(), install.DefaultEngine, install.PullOptions{
			AllowUnpinned: allowUnpinned,
			Progress:      installProgress(),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "install: pull engine:", err)
			return 10
		}
		fmt.Println("\n  engine downloaded to", engineDest)
	}

	if pullModel {
		fmt.Println("  downloading model (this may take a while)...")
		_, err := install.Pull(contextCancellableOnSignal(), install.DefaultModel, install.PullOptions{
			AllowUnpinned: allowUnpinned,
			Progress:      installProgress(),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "install: pull model:", err)
			return 8
		}
		fmt.Println("  model downloaded to", install.ModelsDir())
	}

	fmt.Println("thlibo install complete.")
	return 0
}

// contextCancellableOnSignal returns a background context that is
// cancelled on SIGINT/SIGTERM so Ctrl-C during `thlibo install
// --pull-model` aborts the download cleanly.
func contextCancellableOnSignal() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx
}

// installProgress returns a simple progress printer that writes
// updates to stderr on one carriage-returned line.
func installProgress() install.ProgressFunc {
	start := time.Now()
	return func(written, total int64) {
		pct := "  ?"
		if total > 0 {
			pct = fmt.Sprintf("%3d%%", int((written*100)/total))
		}
		elapsed := time.Since(start).Seconds()
		var speed string
		if elapsed > 0 {
			speed = fmt.Sprintf(" %.1f MiB/s", float64(written)/elapsed/(1<<20))
		}
		fmt.Fprintf(os.Stderr, "\r  model: %s %.1f MiB%s      ",
			pct, float64(written)/(1<<20), speed)
	}
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
