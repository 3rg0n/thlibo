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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/3rg0n/thlibo/internal/adapters/claudecode"
	"github.com/3rg0n/thlibo/internal/adapters/codex"
	"github.com/3rg0n/thlibo/internal/adapters/copilot"
	"github.com/3rg0n/thlibo/internal/adapters/cursor"
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
//
// v0.6.0 note: model + engine downloads moved to inferd. Run
// `inferd install` (or whatever inferd's installer is named) to
// fetch + register the inference daemon. Thlibo install only sets
// up the middleware: hooks, processors, settings.json merge.
func Run(argv []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	var (
		dryRun        bool
		processorsDir string
		hookDir       string
		settingsPath  string
		skipHook      bool
		skipInferd    bool
		inferdVersion string
	)
	fs.BoolVar(&dryRun, "dry-run", false, "report planned actions without applying them")
	fs.StringVar(&processorsDir, "processors-dir", "", "override processors dir (default: ~/.thlibo/processors)")
	fs.StringVar(&hookDir, "hook-dir", "", "override hook dir (default: ~/.thlibo/hooks)")
	fs.StringVar(&settingsPath, "settings", "", "override Claude Code settings path (default: ~/.claude/settings.json)")
	fs.BoolVar(&skipHook, "skip-hook", false, "skip installing the Claude Code hook")
	fs.BoolVar(&skipInferd, "skip-inferd", false, "skip downloading + registering the inferd daemon (middleware-only install)")
	fs.StringVar(&inferdVersion, "inferd-version", "", "pin inferd to a specific tag (default: latest non-prerelease)")
	var installCodex bool
	var codexHooksPath string
	fs.BoolVar(&installCodex, "codex", false, "also install the Codex CLI PostToolUse hook (decision:block substitutes compressed output)")
	fs.StringVar(&codexHooksPath, "codex-hooks", "", "override Codex config.toml path (default: ~/.codex/config.toml)")
	var installCursor bool
	var cursorHooksPath string
	fs.BoolVar(&installCursor, "cursor", false, "also install the Cursor IDE preToolUse hooks (updated_input rewrites the Shell command + Read file_path to compress output)")
	fs.StringVar(&cursorHooksPath, "cursor-hooks", "", "override Cursor hooks.json path (default: ~/.cursor/hooks.json)")
	var installCopilot bool
	var copilotHooksPath string
	fs.BoolVar(&installCopilot, "copilot", false, "also install the GitHub Copilot CLI hooks (preToolUse command rewrite + postToolUse output compression)")
	fs.StringVar(&copilotHooksPath, "copilot-hooks", "", "override Copilot hooks file path (default: ~/.copilot/hooks/thlibo.json)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	// --skip-hook returns before the Codex/Cursor blocks, so pairing it
	// with --codex/--cursor would silently no-op that install. Warn.
	if skipHook && installCodex {
		fmt.Fprintln(os.Stderr, "install: --codex is ignored with --skip-hook (no Codex hook installed). Drop --skip-hook to install it.")
	}
	if skipHook && installCursor {
		fmt.Fprintln(os.Stderr, "install: --cursor is ignored with --skip-hook (no Cursor hook installed). Drop --skip-hook to install it.")
	}
	if skipHook && installCopilot {
		fmt.Fprintln(os.Stderr, "install: --copilot is ignored with --skip-hook (no Copilot hook installed). Drop --skip-hook to install it.")
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

	fmt.Println("thlibo install plan:")
	fmt.Println("  processors dir:", processorsDir)
	fmt.Println("  hook script:   ", hookPath)
	if !skipHook {
		fmt.Println("  settings file: ", settingsPath)
	} else {
		fmt.Println("  settings file:  (skipped)")
	}
	if installCodex {
		cp := codexHooksPath
		if cp == "" && home != "" {
			cp = filepath.Join(home, ".codex", "config.toml")
		}
		fmt.Printf("  codex hooks:    %s (inline)\n", cp)
	} else {
		fmt.Println("  codex hooks:    (skipped; use --codex to install)")
	}
	if installCursor {
		cp := cursorHooksPath
		if cp == "" && home != "" {
			cp = filepath.Join(home, ".cursor", "hooks.json")
		}
		fmt.Printf("  cursor hooks:   %s\n", cp)
	} else {
		fmt.Println("  cursor hooks:   (skipped; use --cursor to install)")
	}
	if installCopilot {
		cp := copilotHooksPath
		if cp == "" && home != "" {
			cp = filepath.Join(home, ".copilot", "hooks", "thlibo.json")
		}
		fmt.Printf("  copilot hooks:  %s\n", cp)
	} else {
		fmt.Println("  copilot hooks:  (skipped; use --copilot to install)")
	}
	if skipInferd {
		fmt.Println("  inferd:         (skipped; --skip-inferd)")
	} else if inferdVersion != "" {
		fmt.Println("  inferd:         pinned to", inferdVersion)
	} else {
		fmt.Println("  inferd:         latest from github.com/3rg0n/inferd/releases")
	}
	if dryRun {
		fmt.Println("  (dry-run: no changes applied)")
		if hint := wslAPEInteropHint(); hint != "" {
			fmt.Println()
			fmt.Println(hint)
		}
		return 0
	}

	// v0.5.x → v0.6.0 exorcism. Idempotent: safe on a fresh
	// install (no-op) and on already-migrated installs (also
	// no-op). Reports its own actions so the user sees what
	// changed.
	if mr, err := install.MigrateFromV05(); err != nil {
		fmt.Fprintln(os.Stderr, "install: migrate v0.5:", err)
		// Non-fatal: keep going. A failed migration shouldn't
		// brick a fresh install on the same box.
	} else if mr.HasWork() {
		fmt.Println("  migrated v0.5.x install:")
		if mr.StoppedAutostart {
			fmt.Println("    - stopped + removed v0.5 daemon autostart")
		}
		if mr.RemovedDaemonBin {
			fmt.Println("    - removed thlibod binary")
		}
		if mr.RemovedEngineBin {
			fmt.Println("    - removed thlibo-engine (llamafile) binary")
		}
		if mr.ModelMovedFrom != "" {
			fmt.Printf("    - moved model %s\n               -> %s\n",
				mr.ModelMovedFrom, mr.ModelMovedTo)
		}
		if mr.RemovedModelsDir {
			fmt.Println("    - cleaned up empty ~/.thlibo/models/")
		}
		if mr.RemovedLogsDir {
			fmt.Println("    - removed daemon log dir ~/.thlibo/logs/")
		}
		for _, n := range mr.Notes {
			fmt.Println("    - note:", n)
		}
	}

	if err := install.MirrorBuiltins(processorsDir); err != nil {
		fmt.Fprintln(os.Stderr, "install: mirror processors:", err)
		return 4
	}
	fmt.Println("  mirrored built-in processors")

	// Sidecar inferd. Failures are non-fatal: thlibo middleware
	// works without inferd (fail-open passthrough per ADR 0006);
	// a failed download just means the user gets passthrough until
	// they retry or install inferd manually.
	if !skipInferd {
		spec := install.InferdInstallSpec{Version: inferdVersion}
		ir, err := install.InstallInferd(spec, install.PullOptions{})
		_ = home // home is reserved for future Mac-specific log-dir setup
		if err != nil {
			fmt.Fprintln(os.Stderr, "install: inferd:", err)
			fmt.Fprintln(os.Stderr, "install: thlibo middleware is fully installed; inferd install failed.")
			fmt.Fprintln(os.Stderr, "install: re-run later or install inferd manually from")
			fmt.Fprintln(os.Stderr, "install: https://github.com/3rg0n/inferd")
		} else {
			reportInferdInstall(ir)
		}
	}

	if skipHook {
		fmt.Println("thlibo install complete.")
		return 0
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

	if installCodex {
		// codexHooksPath (--codex-hooks) historically pointed at
		// hooks.json; it now names the config.toml we write the inline
		// hook into (#170). Accept either the explicit override or the
		// default ~/.codex/config.toml.
		cfgPath := codexHooksPath
		if cfgPath == "" {
			if homeErr != nil {
				fmt.Fprintln(os.Stderr, "install: cannot determine home dir for Codex:", homeErr)
				return 3
			}
			cfgPath = filepath.Join(home, ".codex", "config.toml")
		}
		codexHookPath := filepath.Join(hookDir, "thlibo-rewrite-codex.sh")
		if err := codex.WriteHookScript(codexHookPath); err != nil {
			fmt.Fprintln(os.Stderr, "install: codex hook:", err)
			return 9
		}
		if err := codex.MergeConfigTOMLHook(cfgPath, codexHookPath); err != nil {
			fmt.Fprintln(os.Stderr, "install: codex config.toml hook:", err)
			return 9
		}
		if err := codex.EnableHooksFeatureFlag(cfgPath); err != nil {
			fmt.Fprintln(os.Stderr, "install: codex config.toml:", err)
			return 9
		}
		// Migration: a pre-#170 install put the hook in a sibling
		// hooks.json. Leaving it there recreates the mixed-representation
		// state we just moved off of, so strip any stale thlibo entry
		// from hooks.json next to the config. Non-fatal.
		staleHooksJSON := filepath.Join(filepath.Dir(cfgPath), "hooks.json")
		if err := codex.RemoveStaleHooksJSON(staleHooksJSON); err != nil {
			fmt.Fprintln(os.Stderr, "install: codex hooks.json cleanup (non-fatal):", err)
		}
		fmt.Printf("  wrote Codex hook + added inline [[hooks.PostToolUse]] + [features] hooks=true in %s\n", cfgPath)
		// Codex requires the user to TRUST a command hook before it
		// runs ("Before a non-managed command hook can run, Codex
		// requires you to review and trust the exact hook definition"
		// — developers.openai.com/codex/hooks). The installer can't do
		// this for the user (trust is recorded against the hook's hash,
		// interactively), so until they do it the hook is installed but
		// silent. Surface the one manual step explicitly.
		fmt.Println("  ACTION REQUIRED — trust the hook so Codex will run it:")
		fmt.Println("    Run `/hooks` inside Codex, review the thlibo PostToolUse hook, and approve it.")
		fmt.Println("    Until trusted, Codex installs the hook but won't execute it (compression stays off).")
	}

	if installCursor {
		cp := cursorHooksPath
		if cp == "" {
			if homeErr != nil {
				fmt.Fprintln(os.Stderr, "install: cannot determine home dir for Cursor:", homeErr)
				return 3
			}
			cp = filepath.Join(home, ".cursor", "hooks.json")
		}
		cursorShellHook := filepath.Join(hookDir, "thlibo-rewrite-cursor.sh")
		cursorReadHook := filepath.Join(hookDir, "thlibo-read-cursor.sh")
		if err := cursor.WriteHookScript(cursorShellHook); err != nil {
			fmt.Fprintln(os.Stderr, "install: cursor shell hook:", err)
			return 10
		}
		if err := cursor.WriteReadHookScript(cursorReadHook); err != nil {
			fmt.Fprintln(os.Stderr, "install: cursor read hook:", err)
			return 10
		}
		if err := cursor.MergeHooksJSON(cp, cursorShellHook, cursorReadHook); err != nil {
			fmt.Fprintln(os.Stderr, "install: cursor hooks.json:", err)
			return 10
		}
		fmt.Printf("  wrote Cursor hooks + merged preToolUse/Shell + preToolUse/Read into %s\n", cp)
		// User-level ~/.cursor/hooks.json loads automatically; a
		// project-scoped .cursor/hooks.json only runs in a trusted
		// workspace (cursor.com/docs/hooks). Note the mechanism: Cursor
		// rewrites the Shell command and the Read file_path (so shell
		// output + large-file reads are compressed) but cannot substitute
		// MCP-tool output for built-in tools.
		fmt.Println("    Restart Cursor to load the hooks. Shell output and large file reads (logs, PDFs)")
		fmt.Println("    are compressed; MCP-tool output can't be intercepted. Project hooks need a trusted workspace.")
		// The Read hook bounds `thlibo case` with a timeout so a slow OCR
		// can't hang Cursor. macOS has no `timeout` binary by default
		// (coreutils ships it as `gtimeout`); without it the Read hook
		// safely passes through instead of compressing. Point macOS users
		// at the one-line fix.
		if runtime.GOOS == "darwin" && !hasTimeoutBinary() {
			fmt.Println("    macOS note: install coreutils for file-read compression — `brew install coreutils`")
			fmt.Println("    (provides `gtimeout`; without it the Read hook passes files through uncompressed).")
		}
	}

	if installCopilot {
		cp := copilotHooksPath
		if cp == "" {
			if homeErr != nil {
				fmt.Fprintln(os.Stderr, "install: cannot determine home dir for Copilot:", homeErr)
				return 3
			}
			cp = filepath.Join(home, ".copilot", "hooks", "thlibo.json")
		}
		if err := copilot.WriteHookScripts(hookDir); err != nil {
			fmt.Fprintln(os.Stderr, "install: copilot hook scripts:", err)
			return 11
		}
		if err := copilot.WriteHooksJSON(cp, hookDir); err != nil {
			fmt.Fprintln(os.Stderr, "install: copilot hooks file:", err)
			return 11
		}
		fmt.Printf("  wrote Copilot hooks (preToolUse rewrite + postToolUse compress) to %s\n", cp)
		// Copilot reads every *.json in ~/.copilot/hooks/, each tool owning
		// its own file, so thlibo.json never collides with another tool's.
		// preToolUse rewrites shell commands; postToolUse compresses any
		// tool's verbose output. Both fail safe (preToolUse always allows;
		// postToolUse is fail-open).
		fmt.Println("    Restart Copilot CLI to load the hooks. Shell output is wrapped-and-compressed;")
		fmt.Println("    other verbose tool output is compressed after it runs.")
	}

	if hint := wslAPEInteropHint(); hint != "" {
		fmt.Println()
		fmt.Println(hint)
	}

	fmt.Println("thlibo install complete.")
	return 0
}

// hasTimeoutBinary reports whether a `timeout`/`gtimeout` binary is on
// PATH — the bound the Cursor Read hook needs so a slow OCR can't hang
// the editor. Used only to decide whether to print the macOS coreutils
// hint; the hook itself re-checks at runtime.
func hasTimeoutBinary() bool {
	if _, err := exec.LookPath("timeout"); err == nil {
		return true
	}
	_, err := exec.LookPath("gtimeout")
	return err == nil
}

// reportInferdInstall prints the InferdInstallResult in the same
// indented-bullet format the rest of the installer uses. The output
// shape depends on which branch of the probe-then-delegate state
// machine fired:
//
//   - UsedExisting: thlibo found inferd already running and didn't
//     touch anything.
//   - StartedExisting: thlibo found the binary on disk and started
//     it via the platform's service manager.
//   - InstalledFresh: thlibo downloaded the tarball and ran inferd's
//     bundled installer.
func reportInferdInstall(ir install.InferdInstallResult) {
	switch {
	case ir.UsedExisting:
		if ir.ResolvedVersion != "" {
			fmt.Printf("  inferd %s already running; using existing daemon\n", ir.ResolvedVersion)
		} else {
			fmt.Println("  inferd already running; using existing daemon")
		}
	case ir.StartedExisting:
		if ir.ResolvedVersion != "" {
			fmt.Printf("  inferd %s found installed; started\n", ir.ResolvedVersion)
		} else {
			fmt.Println("  inferd found installed; started")
		}
	case ir.InstalledFresh:
		ver := ir.ResolvedVersion
		if ver == "" {
			ver = "(unknown version)"
		}
		if ir.Reachable {
			fmt.Printf("  inferd %s installed and started (daemon reachable)\n", ver)
		} else {
			fmt.Printf("  inferd %s installed; daemon not reachable yet (see note below)\n", ver)
		}
	}
	if ir.CosignVerified {
		fmt.Println("    - cosign signature verified")
	}
	for _, n := range ir.Notes {
		fmt.Println("    -", n)
	}
}

// wslAPEInteropHint returns a non-empty advisory string when we detect
// that we are running under WSL with the WSLInterop binfmt_misc handler
// active. The llamafile engine is an APE / Cosmopolitan-Libc binary —
// polyglot MZ + ELF — and WSL's binfmt_misc handler matches on the MZ
// header at offset 0, so it grabs the engine and tries to launch it
// through the Windows host instead of running it as a native ELF. The
// daemon then dies with `error: APE is running on WIN32 inside WSL`.
//
// Returns empty on non-WSL and on WSL hosts where the handler has
// already been disabled. The hint is informational only — `thlibo
// install` does not (and should not) attempt the privileged write to
// /proc/sys/fs/binfmt_misc/WSLInterop on the user's behalf.
func wslAPEInteropHint() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	if _, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop"); err != nil {
		return ""
	}
	// Heuristic for WSL: /proc/version mentions "microsoft" or
	// "WSL". Avoids false positives on bare-metal Linux that
	// happens to have a WSLInterop entry from some other source.
	v, err := os.ReadFile("/proc/version") // #nosec G304 -- /proc path, not user input
	if err != nil {
		return ""
	}
	lower := strings.ToLower(string(v))
	if !strings.Contains(lower, "microsoft") && !strings.Contains(lower, "wsl") {
		return ""
	}
	return strings.Join([]string{
		"  WSL detected — one extra step before the daemon can run:",
		"    The llamafile engine is a polyglot APE/Cosmopolitan binary",
		"    (MZ header + ELF body) and WSL's binfmt_misc handler will",
		"    intercept it as a Windows executable. Disable the handler",
		"    (one-time per boot):",
		"",
		"      sudo sh -c 'echo -1 > /proc/sys/fs/binfmt_misc/WSLInterop'",
		"",
		"    Or permanently in /etc/wsl.conf:",
		"",
		"      [interop]",
		"      enabled = false",
		"",
		"    See https://wsl.dev/technical-documentation/interop/",
	}, "\n")
}
