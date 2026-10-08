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
	var o options
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.BoolVar(&o.dryRun, "dry-run", false, "report planned actions without applying them")
	fs.StringVar(&o.processorsDir, "processors-dir", "", "override processors dir (default: ~/.thlibo/processors)")
	fs.StringVar(&o.hookDir, "hook-dir", "", "override hook dir (default: ~/.thlibo/hooks)")
	fs.StringVar(&o.settingsPath, "settings", "", "override Claude Code settings path (default: ~/.claude/settings.json)")
	fs.BoolVar(&o.skipHook, "skip-hook", false, "skip installing the Claude Code hook")
	fs.BoolVar(&o.skipInferd, "skip-inferd", false, "skip downloading + registering the inferd daemon (middleware-only install)")
	fs.StringVar(&o.inferdVersion, "inferd-version", "", "pin inferd to a specific tag (default: latest non-prerelease)")
	clients := optionalClients()
	for _, c := range clients {
		fs.BoolVar(&c.enabled, c.flag, false, c.usage)
		fs.StringVar(&c.override, c.flag+"-hooks", "", c.pathUsage)
	}
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	// --skip-hook returns before the client installs, so pairing it with
	// --codex/--cursor/--copilot would silently no-op that install. Warn.
	for _, c := range clients {
		if o.skipHook && c.enabled {
			fmt.Fprintf(os.Stderr, "install: --%s is ignored with --skip-hook (no %s hook installed). Drop --skip-hook to install it.\n", c.flag, c.name)
		}
	}

	if o.processorsDir == "" {
		o.processorsDir = install.DefaultProcessorsDir()
	}
	o.home, o.homeErr = os.UserHomeDir()
	if o.hookDir == "" {
		if o.homeErr != nil {
			fmt.Fprintln(os.Stderr, "install: cannot determine home dir:", o.homeErr)
			return 3
		}
		o.hookDir = filepath.Join(o.home, ".thlibo", "hooks")
	}
	if o.settingsPath == "" {
		if o.homeErr != nil {
			fmt.Fprintln(os.Stderr, "install: cannot determine home dir:", o.homeErr)
			return 3
		}
		o.settingsPath = filepath.Join(o.home, ".claude", "settings.json")
	}

	printPlan(&o, clients)
	if o.dryRun {
		fmt.Println("  (dry-run: no changes applied)")
		printAutostartHint(o.skipInferd)
		return 0
	}

	// Non-fatal: a failed migration shouldn't brick a fresh install.
	migrateFromV05()

	// Fatal: script processors need a real on-disk directory.
	if err := install.MirrorBuiltins(o.processorsDir); err != nil {
		fmt.Fprintln(os.Stderr, "install: mirror processors:", err)
		return 4
	}
	fmt.Println("  mirrored built-in processors")

	// Non-fatal: the middleware fails open without inferd (ADR 0006).
	if !o.skipInferd {
		installInferd(o.inferdVersion)
	}

	if o.skipHook {
		fmt.Println("thlibo install complete.")
		return 0
	}

	// Fatal from here on: a half-registered client is worse than an
	// error the user can see and re-run.
	if code := installClaudeCode(&o); code != 0 {
		return code
	}
	for _, c := range clients {
		if !c.enabled {
			continue
		}
		path := c.override
		if path == "" {
			if o.homeErr != nil {
				fmt.Fprintf(os.Stderr, "install: cannot determine home dir for %s: %v\n", c.name, o.homeErr)
				return 3
			}
			path = c.defaultPath(o.home)
		}
		if code := c.install(&o, path); code != 0 {
			return code
		}
	}

	printAutostartHint(o.skipInferd)

	fmt.Println("thlibo install complete.")
	return 0
}

// options is the parsed and resolved command line.
type options struct {
	dryRun        bool
	processorsDir string
	hookDir       string
	settingsPath  string
	skipHook      bool
	skipInferd    bool
	inferdVersion string
	home          string
	homeErr       error
}

// client is one of the AI clients installed only on request. Everything
// install does per client — its flags, the --skip-hook warning, its plan
// line, home-dir resolution and the install itself — comes from this
// table, so adding a client is one entry plus its install function.
type client struct {
	flag        string // "codex": gives --codex and --codex-hooks
	name        string // "Codex", for messages
	usage       string
	pathUsage   string
	planLabel   string // aligned label for the plan line
	defaultPath func(home string) string
	// planPath renders the plan line's value; nil prints the path.
	planPath func(path string) string
	install  func(o *options, path string) int

	enabled  bool
	override string
}

// optionalClients is in install order, which is also plan order.
func optionalClients() []*client {
	return []*client{
		{
			flag:        "codex",
			name:        "Codex",
			usage:       "also install the Codex CLI PostToolUse hook (decision:block substitutes compressed output)",
			pathUsage:   "override Codex config.toml path (default: ~/.codex/config.toml)",
			planLabel:   "  codex hooks:    ",
			defaultPath: codex.DefaultConfigPath,
			planPath:    codexPlanPath,
			install:     installCodex,
		},
		{
			flag:        "cursor",
			name:        "Cursor",
			usage:       "also install the Cursor IDE preToolUse hooks (updated_input rewrites the Shell command + Read file_path to compress output)",
			pathUsage:   "override Cursor hooks.json path (default: ~/.cursor/hooks.json)",
			planLabel:   "  cursor hooks:   ",
			defaultPath: cursor.DefaultHooksPath,
			install:     installCursor,
		},
		{
			flag:        "copilot",
			name:        "Copilot",
			usage:       "also install the GitHub Copilot CLI hooks (preToolUse command rewrite + postToolUse output compression)",
			pathUsage:   "override Copilot hooks file path (default: ~/.copilot/hooks/thlibo.json)",
			planLabel:   "  copilot hooks:  ",
			defaultPath: copilot.DefaultHooksPath,
			install:     installCopilot,
		},
	}
}

// printPlan reports what install is about to do. The dry-run output is
// exactly this, so it must describe the real run step for step.
func printPlan(o *options, clients []*client) {
	fmt.Println("thlibo install plan:")
	fmt.Println("  processors dir:", o.processorsDir)
	fmt.Println("  hook script:   ", claudecode.HookPaths(o.hookDir).BashExecHook)
	if !o.skipHook {
		fmt.Println("  settings file: ", o.settingsPath)
	} else {
		fmt.Println("  settings file:  (skipped)")
	}
	for _, c := range clients {
		if !c.enabled {
			fmt.Printf("%s(skipped; use --%s to install)\n", c.planLabel, c.flag)
			continue
		}
		// Unlike the install step, the plan doesn't fail on an unknown
		// home: it prints what it has.
		path := c.override
		if path == "" && o.home != "" {
			path = c.defaultPath(o.home)
		}
		if path != "" && c.planPath != nil {
			fmt.Printf("%s%s\n", c.planLabel, c.planPath(path))
		} else {
			fmt.Printf("%s%s\n", c.planLabel, path)
		}
	}
	if o.skipInferd {
		fmt.Println("  inferd:         (skipped; --skip-inferd)")
	} else if o.inferdVersion != "" {
		fmt.Println("  inferd:         pinned to", o.inferdVersion)
	} else {
		fmt.Println("  inferd:         latest from github.com/3rg0n/inferd/releases")
	}
}

// codexPlanPath reports the representation install will actually write.
// That depends on what the config layer already uses (see
// codex.InstallHook), so it is detected rather than assumed inline.
func codexPlanPath(cfgPath string) string {
	hj := filepath.Join(filepath.Dir(cfgPath), "hooks.json")
	if codex.DetectRepresentation(cfgPath, hj) == codex.RepHooksJSON {
		return hj + " (hooks.json — this layer's representation)"
	}
	return cfgPath + " (inline)"
}

// migrateFromV05 is the v0.5.x → v0.6.0 exorcism. Idempotent: a no-op on a
// fresh install and on an already-migrated one. Reports its own actions so
// the user sees what changed. Non-fatal by design.
func migrateFromV05() {
	mr, err := install.MigrateFromV05()
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: migrate v0.5:", err)
		return
	}
	if !mr.HasWork() {
		return
	}
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

// installInferd fetches and registers the sidecar. Non-fatal: thlibo
// works without inferd (fail-open passthrough per ADR 0006); a failed
// download just means passthrough until the user retries or installs
// inferd manually.
func installInferd(version string) {
	ir, err := install.InstallInferd(install.InferdInstallSpec{Version: version}, install.PullOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: inferd:", err)
		fmt.Fprintln(os.Stderr, "install: thlibo middleware is fully installed; inferd install failed.")
		fmt.Fprintln(os.Stderr, "install: re-run later or install inferd manually from")
		fmt.Fprintln(os.Stderr, "install: https://github.com/3rg0n/inferd")
		return
	}
	reportInferdInstall(ir)
}

// claudeHook is one Claude Code hook script: how to write it and how to
// report the result. The labels differ between the success and conflict
// lines, and only the Exec hooks print the follow-up, so they are data
// rather than derived.
type claudeHook struct {
	write    func(path string) (claudecode.WriteResult, error)
	path     string
	errCtx   string // "install: write <errCtx>:"
	label    string // success line: "  <label><result>"
	conflict string // conflict line: "  <conflict>your edits preserved — ..."
	followUp string // printed after a conflict line, if set
}

// installClaudeCode writes the six hook scripts, registers them in
// settings.json and mirrors the /caselog skill. Hook scripts are
// SHA-stamped, so a user-edited script is left alone and the new version
// lands beside it as .new (CLAUDE.md invariant #6).
//
// The Bash and PowerShell variants are both installed unconditionally:
// Claude Code only invokes the matcher it actually uses (the PowerShell
// tool is CLAUDE_CODE_USE_POWERSHELL_TOOL=1), so an unused hook just sits
// on disk. The Read hooks rewrite tool_input.file_path to a `thlibo case`
// summary for large log-shaped files. The Write/Edit hooks are installed
// but config-gated at runtime: a fresh install never rewrites the user's
// files until auto_shorthand_on_write is enabled in ~/.thlibo/config.yaml.
func installClaudeCode(o *options) int {
	paths := claudecode.HookPaths(o.hookDir)
	for _, h := range []claudeHook{
		{claudecode.WriteHookScript, paths.BashExecHook, "hook",
			"Bash hook script: ", "Bash hook: ",
			"             review and merge manually, then remove the .new file."},
		{claudecode.WriteHookScriptPS1, paths.PS1ExecHook, "ps1 hook",
			"PowerShell hook script: ", "PowerShell hook: ",
			"                   review and merge manually, then remove the .new file."},
		{claudecode.WriteHookReadScript, paths.BashReadHook, "read hook",
			"Read hook (bash): ", "Read hook (bash): ", ""},
		{claudecode.WriteHookReadScriptPS1, paths.PS1ReadHook, "read ps1 hook",
			"Read hook (ps1):  ", "Read hook (ps1):  ", ""},
		{claudecode.WriteHookWriteScript, paths.BashWriteHook, "write hook",
			"Write hook (bash): ", "Write hook (bash): ", ""},
		{claudecode.WriteHookWriteScriptPS1, paths.PS1WriteHook, "write ps1 hook",
			"Write hook (ps1):  ", "Write hook (ps1):  ", ""},
	} {
		res, err := h.write(h.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "install: write %s: %v\n", h.errCtx, err)
			return 5
		}
		if res != claudecode.WriteResultConflict {
			fmt.Printf("  %s%s\n", h.label, res)
			continue
		}
		fmt.Printf("  %syour edits preserved — new version written to %s.new\n", h.conflict, h.path)
		if h.followUp != "" {
			fmt.Println(h.followUp)
		}
	}

	if err := claudecode.MergeSettingsAll(o.settingsPath, paths); err != nil {
		fmt.Fprintln(os.Stderr, "install: merge settings:", err)
		return 6
	}
	fmt.Println("  merged Claude Code settings.json (Bash + PowerShell + Read + Write/Edit matchers)")
	fmt.Println("  Write/Edit auto-shorthand is OFF by default; enable in ~/.thlibo/config.yaml")

	// Mirror the /caselog skill into ~/.claude/skills/caselog/, with the
	// same SHA-stamp / conflict semantics as the hooks.
	skillsDir := filepath.Join(filepath.Dir(o.settingsPath), "skills")
	skillResult, err := claudecode.InstallCaselogSkill(skillsDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: write /caselog skill:", err)
		return 6
	}
	if skillResult == claudecode.WriteResultConflict {
		target := filepath.Join(skillsDir, "caselog", "SKILL.md")
		fmt.Printf("  /caselog skill: your edits preserved — new version at %s.new\n", target)
	} else {
		fmt.Printf("  /caselog skill: %s\n", skillResult)
	}
	return 0
}

// installCodex installs the Codex PostToolUse hook into cfgPath, the
// --codex-hooks override or ~/.codex/config.toml.
func installCodex(o *options, cfgPath string) int {
	// codexHooksPath (--codex-hooks) historically pointed at
	// hooks.json; it now names the config.toml we write the inline
	// hook into (#170). Accept either the explicit override or the
	// default ~/.codex/config.toml.
	// Host picks the script: .ps1 on Windows, .sh elsewhere. A bare
	// .sh path in a hook command is resolved through the .sh file
	// association on Windows, which on a Git-for-Windows box launches
	// git-bash.exe instead of interpreting the script (#127 for Claude
	// Code, #126 here).
	codexHookPath := filepath.Join(o.hookDir, codex.HookFileName())
	if err := codex.WriteHookScript(codexHookPath); err != nil {
		fmt.Fprintln(os.Stderr, "install: codex hook:", err)
		return 9
	}
	// Write the hook into whichever representation this config layer
	// already uses — inline by default, hooks.json when that's where
	// the layer's other hooks live. Mixing the two in one layer makes
	// Codex warn and hide hooks (#170), in both directions.
	hooksJSON := filepath.Join(filepath.Dir(cfgPath), "hooks.json")
	rep, err := codex.InstallHook(cfgPath, hooksJSON, codexHookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install: codex hook:", err)
		return 9
	}
	// The feature flag lives in config.toml either way — without it
	// Codex ignores every hook it finds, wherever it's declared.
	if err := codex.EnableHooksFeatureFlag(cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "install: codex config.toml:", err)
		return 9
	}
	if rep == codex.RepInline {
		// Migration: a pre-#170 install put the hook in a sibling
		// hooks.json. Leaving it there recreates the mixed-representation
		// state we just moved off of, so strip any stale thlibo entry
		// from hooks.json next to the config. Non-fatal. Skipped on the
		// hooks.json path, where that entry is the one we just wrote.
		if err := codex.RemoveStaleHooksJSON(hooksJSON); err != nil {
			fmt.Fprintln(os.Stderr, "install: codex hooks.json cleanup (non-fatal):", err)
		}
		fmt.Printf("  wrote Codex hook + added inline [[hooks.PostToolUse]] + [features] hooks=true in %s\n", cfgPath)
	} else {
		fmt.Printf("  wrote Codex hook + added PostToolUse entry in %s (the representation this layer already uses) + [features] hooks=true in %s\n", hooksJSON, cfgPath)
	}
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
	// A clean install plus a `/hooks` approval reads as "compression is
	// on", and on Windows that is currently false: Codex does not
	// deliver PostToolUse to a trusted hook for its shell results, so
	// the hook never runs and fail-open hides it (#126, upstream
	// openai/codex#38850). Say so rather than let the success output
	// imply otherwise.
	if runtime.GOOS == "windows" {
		fmt.Println("  WARNING: on Windows, Codex does not currently deliver PostToolUse to the")
		fmt.Println("    hook for its shell results, so compression stays off even once trusted.")
		fmt.Println("    Tracked upstream: github.com/openai/codex/issues/38850 (thlibo #126).")
		fmt.Println("    The hook itself is installed and correct; nothing to do here but wait.")
	}
	return 0
}

// installCursor installs the Cursor preToolUse Shell + Read hooks into cp.
func installCursor(o *options, cp string) int {
	cursorShellHook := filepath.Join(o.hookDir, "thlibo-rewrite-cursor.sh")
	cursorReadHook := filepath.Join(o.hookDir, "thlibo-read-cursor.sh")
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
	return 0
}

// installCopilot writes thlibo's own Copilot hooks file at cp.
func installCopilot(o *options, cp string) int {
	if err := copilot.WriteHookScripts(o.hookDir); err != nil {
		fmt.Fprintln(os.Stderr, "install: copilot hook scripts:", err)
		return 11
	}
	if err := copilot.WriteHooksJSON(cp, o.hookDir); err != nil {
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
	return 0
}

// printAutostartHint emits the autostart warning, if there is one, just
// ahead of the closing line — the whole point being that an install can
// read as complete while the daemon is never going to start. Shared by
// the dry-run and real paths so a plan and the install it describes
// cannot disagree about whether the warning applies.
//
// Suppressed under --skip-inferd: there, an unstarted daemon is what the
// user asked for, so the warning would be noise.
func printAutostartHint(skipInferd bool) {
	if skipInferd {
		return
	}
	if hint := linuxUserServiceHint(); hint != "" {
		fmt.Println()
		fmt.Println(hint)
	}
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

// linuxUserServiceHint returns a non-empty advisory when we are
// installing on Linux without a working systemd user session.
//
// This is the one Linux-specific failure the installer cannot fix and
// must not hide. inferd autostarts via a systemd *user* unit, and
// install.Install deliberately swallows `systemctl --user` errors so a
// systemd-less host still gets the unit file on disk for a later
// session. The cost of that choice is silence: the unit is written,
// nothing ever starts it, the daemon never listens, and thlibo's
// fail-open path (invariant #2) then makes every hook a permanent
// passthrough — compression quietly does nothing and no error is ever
// printed. The user has no way to tell that from "working."
//
// Two distinct causes, because the remedies differ:
//
//   - no systemd at all (container, sysvinit, musl distro) — the unit
//     is inert; inferd has to be started by hand or by another
//     supervisor.
//   - systemd running but no per-user bus — `systemctl --user` cannot
//     connect, so enable/start silently did nothing. Measured on WSL
//     Ubuntu 26.04, where user@$UID.service fails and /run/user/$UID/bus
//     is absent while the system manager reports "running".
//
// Detection is filesystem-only and never shells out: the installer
// already ran systemctl and got no usable signal from it.
func linuxUserServiceHint() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	// /run/systemd/system is systemd's own "am I the init system?"
	// marker — the check systemd documents for exactly this question.
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return strings.Join([]string{
			"  systemd not detected — inferd will not autostart:",
			"    inferd's autostart is a systemd user unit, and nothing on this",
			"    host will read one, so the daemon never starts and every hook",
			"    falls open (passes output through uncompressed, silently).",
			"",
			"    Start inferd yourself, or supervise it however this host",
			"    manages services:",
			"",
			"      inferd &",
			"",
			"    Then check it is listening:",
			"",
			"      inferdctl doctor",
		}, "\n")
	}
	if userBusPresent() {
		return ""
	}
	return strings.Join([]string{
		"  no systemd user session — inferd will not autostart:",
		"    `systemctl --user` cannot reach a per-user bus on this host, so",
		"    enabling the unit did nothing. The daemon never starts and every",
		"    hook falls open (passes output through uncompressed, silently).",
		"",
		"    Diagnose the user manager:",
		"",
		"      systemctl status user@$(id -u).service",
		"      journalctl --user -xe",
		"",
		"    If units run while you are logged in but stop afterwards, enable",
		"    lingering instead:",
		"",
		"      loginctl enable-linger $USER",
		"",
		"    Until then, start the daemon by hand with `inferd &`.",
	}, "\n")
}

// userBusPresent reports whether a per-user D-Bus socket exists — the
// transport `systemctl --user` requires.
//
// $DBUS_SESSION_BUS_ADDRESS is resolved to a path and that path is
// stat'd, never trusted for merely being set. Measured on WSL Ubuntu
// 26.04: the variable is exported as unix:path=/run/user/1000/bus while
// that socket does not exist, because the session leader sets it before
// (and independently of) the user manager coming up. Treating a set
// variable as proof of a bus reports healthy on exactly the host this
// hint exists to warn about.
//
// Only unix socket paths are checkable. An abstract-namespace or tcp
// address has no filesystem entry, so those are taken at face value —
// the caller's alternative is a false warning on a working host.
//
// Both os.Stat calls below read paths out of the invoking user's own
// environment. gosec flags that as tainted (G703), but the paths are
// only ever stat'd — never opened, written, or joined with anything
// attacker-supplied — and the sole observable effect is whether an
// advisory prints. A user who edits their own $DBUS_SESSION_BUS_ADDRESS
// can suppress their own hint; there is nothing else to reach.
func userBusPresent() bool {
	if addr := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); addr != "" {
		path, ok := unixSocketPath(addr)
		if !ok {
			return true // not a checkable unix path; assume the host knows
		}
		_, err := os.Stat(path) // #nosec G703 -- stat-only existence check; see above
		return err == nil
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	_, err := os.Stat(filepath.Join(dir, "bus")) // #nosec G703 -- stat-only; see above
	return err == nil
}

// unixSocketPath extracts the filesystem path from a D-Bus address,
// reporting false when the address names no such path. The format is
// semicolon-separated addresses of `transport:key=value,key=value`; we
// want the first `unix:` entry's `path=`.
func unixSocketPath(addr string) (string, bool) {
	for _, one := range strings.Split(addr, ";") {
		if !strings.HasPrefix(one, "unix:") {
			continue
		}
		for _, kv := range strings.Split(strings.TrimPrefix(one, "unix:"), ",") {
			if v, ok := strings.CutPrefix(kv, "path="); ok && v != "" {
				return v, true
			}
		}
	}
	return "", false
}
