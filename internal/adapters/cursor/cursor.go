// Package cursor installs thlibo's preToolUse hook into Cursor IDE's
// configuration.
//
// Cursor cannot substitute a shell command's *output* — its
// afterShellExecution hook is observe-only and updated_mcp_tool_output
// (postToolUse) applies to MCP tools only (cursor.com/docs/hooks). But
// preToolUse CAN rewrite the Shell tool's *input* via `updated_input`.
// So thlibo uses the same command-wrap mechanism it uses for Claude
// Code's Bash tool: `thlibo rewrite` turns `git status` into
// `thlibo exec -- git status`, which runs the command and routes its
// output through the middleware before the model reads it. Different
// mechanism from Codex's PostToolUse decision:block, same observable
// effect for shell commands.
//
// The package installs two things:
//
//  1. The hook script (thlibo-rewrite-cursor.sh) to the user's hook dir.
//  2. A hooks.json entry: preToolUse/matcher "Shell"/command→<hook>,
//     merged into ~/.cursor/hooks.json without clobbering other events.
//
// Cursor has no separate feature-flag file — a hooks.json in a loaded
// config layer (user ~/.cursor, or a trusted project .cursor) is
// sufficient. User-level hooks load automatically; project-level hooks
// require the workspace to be trusted.
package cursor

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed hook.sh
var hookScript []byte

//go:embed hook-read.sh
var readHookScript []byte

// HookScript returns the embedded Cursor Shell-tool hook script bytes.
func HookScript() []byte { return hookScript }

// ReadHookScript returns the embedded Cursor Read-tool hook script bytes.
func ReadHookScript() []byte { return readHookScript }

// WriteHookScript writes the Shell-tool hook script to path (0o700).
func WriteHookScript(path string) error { return writeScript(path, hookScript) }

// WriteReadHookScript writes the Read-tool hook script to path (0o700).
func WriteReadHookScript(path string) error { return writeScript(path, readHookScript) }

func writeScript(path string, script []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("cursor: create hook dir: %w", err)
	}
	if err := os.WriteFile(path, script, 0o600); err != nil {
		return fmt.Errorf("cursor: write hook: %w", err)
	}
	// #nosec G302 -- owner-execute required; group/other remain 0.
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("cursor: chmod hook: %w", err)
	}
	return nil
}

// Filename markers used to recognise a previously-installed thlibo entry
// and update it in place (one per tool the hooks cover).
const (
	shellHookMarker = "thlibo-rewrite-cursor.sh"
	readHookMarker  = "thlibo-read-cursor.sh"
)

// Cursor matches preToolUse by tool type ("Shell", "Read", "Write", …).
const (
	shellMatcher = "Shell"
	readMatcher  = "Read"
)

// MergeHooksJSON loads hooksPath, adds thlibo's preToolUse entries — one
// for the "Shell" tool (shellHookPath, command rewrite) and one for the
// "Read" tool (readHookPath, file_path rewrite) — and writes back. Every
// unrelated key and every other hook entry is preserved.
//
// The Cursor hooks.json schema is:
//
//	{ "version": 1, "hooks": { "preToolUse": [
//	    { "matcher": "Shell", "command": "<shell-hook>" },
//	    { "matcher": "Read",  "command": "<read-hook>" } ] } }
//
// Idempotent; recognises prior installs by each hook's filename marker
// and updates them in place; refuses to overwrite malformed JSON so
// corruption is never silent.
func MergeHooksJSON(hooksPath, shellHookPath, readHookPath string) error {
	shellHookPath = normalisePath(shellHookPath)
	readHookPath = normalisePath(readHookPath)

	var root map[string]any
	buf, err := os.ReadFile(hooksPath) // #nosec G304 -- hooksPath is chosen by the installer, not user input.
	switch {
	case err == nil:
		if len(buf) == 0 {
			root = map[string]any{}
		} else {
			if err := json.Unmarshal(buf, &root); err != nil {
				return fmt.Errorf("cursor: parse %s: %w", hooksPath, err)
			}
		}
	case os.IsNotExist(err):
		root = map[string]any{}
	default:
		return fmt.Errorf("cursor: read %s: %w", hooksPath, err)
	}

	// Cursor requires a top-level schema version; default to 1 without
	// clobbering a version the user (or a newer Cursor) already set.
	if _, ok := root["version"]; !ok {
		root["version"] = 1
	}

	upsertPreToolUseHook(root, shellMatcher, shellHookMarker, shellHookPath)
	upsertPreToolUseHook(root, readMatcher, readHookMarker, readHookPath)

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("cursor: marshal hooks: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return fmt.Errorf("cursor: create hooks dir: %w", err)
	}
	if err := os.WriteFile(hooksPath, encoded, 0o600); err != nil {
		return fmt.Errorf("cursor: write %s: %w", hooksPath, err)
	}
	return nil
}

// upsertPreToolUseHook adds (or updates in place) the thlibo preToolUse
// entry identified by marker, setting its matcher + command. Cursor's
// preToolUse entries are flat objects ({matcher, command, ...}), not the
// nested {matcher, hooks:[...]} shape Codex/Claude Code use. A prior
// entry is recognised by its filename marker so reinstalls are
// idempotent and each tool's hook is tracked independently.
func upsertPreToolUseHook(root map[string]any, matcher, marker, hookPath string) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}

	pre, _ := hooks["preToolUse"].([]any)
	if pre == nil {
		pre = []any{}
	}

	command := hookCommand(hookPath)
	for i, h := range pre {
		obj, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := obj["command"].(string)
		if strings.Contains(normalisePath(cmd), marker) {
			obj["command"] = command
			obj["matcher"] = matcher
			pre[i] = obj
			hooks["preToolUse"] = pre
			return
		}
	}

	pre = append(pre, map[string]any{"matcher": matcher, "command": command})
	hooks["preToolUse"] = pre
}

// RemoveHooks undoes MergeHooksJSON + WriteHookScript: it strips thlibo's
// preToolUse entries from hooksPath and deletes both hook scripts from
// hookDir.
//
// Deleting the scripts without unregistering them is the worse of the two
// half-jobs, and leaving them registered is the other: Cursor keeps
// running a hook whose command it still holds, so thlibo would keep
// rewriting Shell commands and Read paths after uninstall reported
// complete (#137). Both halves happen here so neither can be forgotten.
//
// Each step is independent — a failure on one is recorded and the rest
// still run.
func RemoveHooks(hooksPath, hookDir string) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	record(removeHooksJSONEntries(hooksPath))
	if hookDir != "" {
		for _, name := range []string{shellHookMarker, readHookMarker} {
			if err := os.Remove(filepath.Join(hookDir, name)); err != nil && !os.IsNotExist(err) {
				record(err)
			}
		}
	}
	if firstErr != nil {
		return fmt.Errorf("cursor: remove hooks: %w", firstErr)
	}
	return nil
}

// removeHooksJSONEntries drops every hook entry naming one of thlibo's
// scripts, from every event — not just preToolUse, so an entry a past
// version registered elsewhere goes too.
//
// No-ops when the file is absent or holds no thlibo marker. Leaves a
// malformed file untouched (returning nil) rather than risk clobbering
// user data: the same call thlibo makes on install refuses to parse it
// too, so the user's own editor is the right place to fix it.
//
// Unrelated keys survive, including Cursor's required top-level "version".
// That means the file is left as a `{"version": 1}` husk when thlibo's
// were the only hooks in it. The husk declares nothing and Cursor treats
// it as no hooks; deleting a file thlibo does not own is the bigger
// action, so it stays.
func removeHooksJSONEntries(hooksPath string) error {
	buf, err := os.ReadFile(hooksPath) // #nosec G304 -- installer-chosen path, not user input.
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cursor: read %s: %w", hooksPath, err)
	}
	if len(buf) == 0 || !containsThliboMarker(string(buf)) {
		return nil
	}

	var root map[string]any
	if err := json.Unmarshal(buf, &root); err != nil {
		return nil
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return nil
	}

	changed := false
	for event, entries := range hooks {
		arr, ok := entries.([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(arr))
		for _, h := range arr {
			obj, ok := h.(map[string]any)
			if ok {
				if cmd, _ := obj["command"].(string); containsThliboMarker(cmd) {
					changed = true
					continue
				}
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 {
			delete(hooks, event)
			continue
		}
		hooks[event] = kept
	}
	if !changed {
		return nil
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("cursor: marshal %s: %w", hooksPath, err)
	}
	if err := os.WriteFile(hooksPath, encoded, 0o600); err != nil {
		return fmt.Errorf("cursor: write %s: %w", hooksPath, err)
	}
	return nil
}

// containsThliboMarker reports whether s names either hook script. Both
// names are always checked: a command string carries only the file, so
// matching one name would leave the other registered and firing.
func containsThliboMarker(s string) bool {
	s = normalisePath(s)
	return strings.Contains(s, shellHookMarker) || strings.Contains(s, readHookMarker)
}

// hookCommand builds the hooks.json "command" string for a hook script.
//
// Cursor hands this string to the OS to execute. On Unix the script's
// shebang runs it directly. On Windows there is no shebang and no `.sh`
// file association, so a bare path pops the "Select an app to open this
// .sh file" dialog and the hook never runs — Cursor must be told to run
// it through bash. We locate Git-for-Windows / MSYS bash and emit
// `"<bash>" "<hook>"` (quoted for the spaces in "Program Files"), the
// same shape other Windows Cursor hooks (e.g. taco) use.
func hookCommand(hookPath string) string {
	hookPath = normalisePath(hookPath)
	if runtime.GOOS != "windows" {
		return hookPath
	}
	bash := findBashWindows()
	if bash == "" {
		// No bash found — emit the bare path. The hook can't run without
		// bash anyway; this keeps install non-fatal and lets a
		// PATH-resolvable bash (if any) still pick it up.
		return hookPath
	}
	return `"` + normalisePath(bash) + `" "` + hookPath + `"`
}

// findBashWindows returns a bash.exe path on Windows, or "" if none is
// found. Prefers Git for Windows' bash; falls back to PATH.
func findBashWindows() string {
	candidates := []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		candidates = append(candidates, filepath.Join(pf, "Git", "bin", "bash.exe"))
	}
	if lad := os.Getenv("LOCALAPPDATA"); lad != "" {
		candidates = append(candidates, filepath.Join(lad, "Programs", "Git", "bin", "bash.exe"))
	}
	for _, c := range candidates {
		// #nosec G703 -- c is a literal Git-install subpath joined onto a
		// trusted OS env dir (ProgramFiles/LOCALAPPDATA), not user input;
		// we only Stat it to pick an existing bash.exe.
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	// Last resort: PATH lookup (covers non-standard Git installs). Skip a
	// WSL bash (System32\bash.exe / a WSL shim): it runs under a Linux
	// filesystem view and can't execute a `C:/Users/...` hook path, so
	// it would fail at runtime — better to return "" (bare path, install
	// stays non-fatal) than wire a bash that can't run the hook.
	if p, err := exec.LookPath("bash"); err == nil {
		lower := strings.ToLower(normalisePath(p))
		if !strings.Contains(lower, "/system32/") && !strings.Contains(lower, "/windows/") {
			return p
		}
	}
	return ""
}

// normalisePath converts backslashes to forward slashes so bash -c
// doesn't eat them on Windows. Same fix the claudecode/codex adapters
// apply to their hook paths.
func normalisePath(p string) string {
	if !strings.ContainsRune(p, '\\') {
		return p
	}
	return strings.ReplaceAll(p, "\\", "/")
}
