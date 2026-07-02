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
//	1. The hook script (thlibo-rewrite-cursor.sh) to the user's hook dir.
//	2. A hooks.json entry: preToolUse/matcher "Shell"/command→<hook>,
//	   merged into ~/.cursor/hooks.json without clobbering other events.
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
	"path/filepath"
	"strings"
)

//go:embed hook.sh
var hookScript []byte

// HookScript returns the embedded Cursor hook script bytes.
func HookScript() []byte { return hookScript }

// WriteHookScript writes the hook script to path (0o700, owner-only).
func WriteHookScript(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("cursor: create hook dir: %w", err)
	}
	if err := os.WriteFile(path, hookScript, 0o600); err != nil {
		return fmt.Errorf("cursor: write hook: %w", err)
	}
	// #nosec G302 -- owner-execute required; group/other remain 0.
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("cursor: chmod hook: %w", err)
	}
	return nil
}

// hookMarker is the filename substring we use to recognise a
// previously-installed thlibo entry and update it in place.
const hookMarker = "thlibo-rewrite-cursor.sh"

// shellMatcher scopes the preToolUse hook to the built-in Shell tool
// (Cursor matches preToolUse by tool type: "Shell", "Read", "Write", …).
const shellMatcher = "Shell"

// MergeHooksJSON loads hooksPath, adds a preToolUse/"Shell" entry
// pointing at hookPath, and writes back. Every unrelated key and every
// other hook entry is preserved.
//
// The Cursor hooks.json schema is:
//
//	{ "version": 1, "hooks": { "preToolUse": [ { "matcher": "Shell",
//	  "command": "<hook>" } ] } }
//
// Idempotent; recognises a prior install by the hookMarker substring;
// refuses to overwrite malformed JSON so corruption is never silent.
func MergeHooksJSON(hooksPath, hookPath string) error {
	hookPath = normalisePath(hookPath)

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

	addPreToolUseHook(root, hookPath)

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

// addPreToolUseHook walks/creates hooks.preToolUse[?matcher=="Shell"]
// and appends (or updates in-place) the thlibo entry. Cursor's
// preToolUse entries are flat objects ({matcher, command, ...}), not the
// nested {matcher, hooks:[...]} shape Codex/Claude Code use.
func addPreToolUseHook(root map[string]any, hookPath string) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}

	pre, _ := hooks["preToolUse"].([]any)
	if pre == nil {
		pre = []any{}
	}

	// Update in place if a thlibo entry is already present (idempotent
	// reinstall), regardless of its matcher.
	for i, h := range pre {
		obj, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := obj["command"].(string)
		if strings.Contains(normalisePath(cmd), hookMarker) {
			obj["command"] = hookPath
			obj["matcher"] = shellMatcher
			pre[i] = obj
			hooks["preToolUse"] = pre
			return
		}
	}

	pre = append(pre, map[string]any{"matcher": shellMatcher, "command": hookPath})
	hooks["preToolUse"] = pre
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
