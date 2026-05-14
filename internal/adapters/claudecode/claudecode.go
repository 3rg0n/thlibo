// Package claudecode generates and installs the PreToolUse hook
// Claude Code uses to invoke thlibo. The hook script is embedded
// at build time and laid down on disk at install time; the settings
// merger adds a PreToolUse entry matching the Bash tool without
// clobbering any existing hooks.
//
// Settings shape (see Claude Code hooks docs):
//
//	{
//	  "hooks": {
//	    "PreToolUse": [
//	      {
//	        "matcher": "Bash",
//	        "hooks": [
//	          { "type": "command", "command": "/path/to/thlibo-rewrite.sh" }
//	        ]
//	      }
//	    ]
//	  }
//	}
package claudecode

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

//go:embed hook.ps1
var hookScriptPS1 []byte

// HookScript returns the embedded Bash hook script bytes, unmodified.
// Exposed so the installer can write it to disk and tests can
// assert on its shape without running an install.
func HookScript() []byte { return hookScript }

// HookScriptPS1 returns the embedded PowerShell hook script bytes,
// unmodified. Companion to HookScript for Windows installs where
// CLAUDE_CODE_USE_POWERSHELL_TOOL=1 routes tool calls through the
// PowerShell tool instead of Bash.
func HookScriptPS1() []byte { return hookScriptPS1 }

// WriteHookScript writes the Bash hook script to path. See writeHookBytes
// for the mode/permission details.
func WriteHookScript(path string) error {
	return writeHookBytes(path, hookScript)
}

// WriteHookScriptPS1 writes the PowerShell hook script to path. Same
// permission semantics as WriteHookScript; PowerShell ignores the
// POSIX execute bit but respects the ACL-equivalent on Windows.
func WriteHookScriptPS1(path string) error {
	return writeHookBytes(path, hookScriptPS1)
}

// writeHookBytes writes b to path, creating parent directories as
// needed. The file is created with a restrictive mode (0o600) and
// then chmod'd to 0o700 so the owner can execute it. Group and world
// have no access - this is user-scoped tooling. On Windows the execute
// bit has no meaning; the file is still written with 0o600-equivalent
// ACLs through Go's os.WriteFile.
func writeHookBytes(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("claudecode: create hook dir: %w", err)
	}
	// Two-step: WriteFile respects the gosec G306 guidance (0o600),
	// then chmod adds the owner-execute bit needed for the hook to
	// run. Group/other stay at 0 — nothing about this script is
	// meant to be shared.
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("claudecode: write hook: %w", err)
	}
	// #nosec G302 -- owner-execute bit is required so the hook can
	// run; gosec's default 0o600 ceiling doesn't fit executables.
	// Group/other remain at 0, which is the point of G302.
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("claudecode: chmod hook: %w", err)
	}
	return nil
}

// HookEntry is what the PreToolUse/Bash matcher's `hooks` array
// contains for thlibo. One entry per install; MergeSettings is
// idempotent and won't add a duplicate.
type HookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// Our entry markers: fixed strings in the command path that let
// MergeSettings recognise a previous install so re-running the
// installer doesn't pile up duplicate entries. Matching on a suffix
// rather than the full path survives user-initiated moves.
const (
	hookMarker    = "thlibo-rewrite.sh"
	hookMarkerPS1 = "thlibo-rewrite.ps1"
)

// MergeSettings loads settingsPath, adds a PreToolUse/Bash hook
// pointing at bashHookPath, and writes the file back. Preserves every
// other key and every other hook entry verbatim.
//
// Deprecated: use MergeSettingsFull to also register the PowerShell
// hook on Windows where CLAUDE_CODE_USE_POWERSHELL_TOOL=1 is set.
// Kept for compatibility with existing installers and tests.
func MergeSettings(settingsPath, hookPath string) error {
	return MergeSettingsFull(settingsPath, hookPath, "")
}

// MergeSettingsFull loads settingsPath (creating an empty object if
// the file doesn't exist), adds a PreToolUse hook entry for each
// non-empty hook path, and writes the file back. bashHookPath is
// registered under matcher "Bash"; ps1HookPath, if non-empty, is
// registered under matcher "PowerShell" — which is the tool name
// Claude Code uses when CLAUDE_CODE_USE_POWERSHELL_TOOL=1.
// Preserves every other key and every other hook entry verbatim.
//
// Idempotent: calling it twice on the same settings file leaves at
// most one thlibo hook entry per matcher. If settingsPath is invalid
// JSON, returns an error without modifying anything.
func MergeSettingsFull(settingsPath, bashHookPath, ps1HookPath string) error {
	var root map[string]any
	buf, err := os.ReadFile(settingsPath) // #nosec G304 -- path is a thlibo config location chosen by the installer
	switch {
	case err == nil:
		if len(buf) == 0 {
			root = map[string]any{}
		} else {
			if err := json.Unmarshal(buf, &root); err != nil {
				return fmt.Errorf("claudecode: parse %s: %w", settingsPath, err)
			}
		}
	case os.IsNotExist(err):
		root = map[string]any{}
	default:
		return fmt.Errorf("claudecode: read %s: %w", settingsPath, err)
	}

	if bashHookPath != "" {
		addPreToolUseHook(root, "Bash", bashHookPath, hookMarker)
	}
	if ps1HookPath != "" {
		addPreToolUseHook(root, "PowerShell", ps1HookPath, hookMarkerPS1)
	}

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("claudecode: marshal settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		return fmt.Errorf("claudecode: create settings dir: %w", err)
	}
	if err := os.WriteFile(settingsPath, encoded, 0o600); err != nil {
		return fmt.Errorf("claudecode: write %s: %w", settingsPath, err)
	}
	return nil
}

// RemoveHooks loads settingsPath and removes every thlibo-authored
// PreToolUse hook entry (recognised by the hookMarker / hookMarkerPS1
// suffix in the command string). Empty matcher groups and an empty
// PreToolUse array are cleaned up so the JSON stays tidy. Preserves
// every unrelated key. Returns nil if the file doesn't exist.
//
// Companion to MergeSettingsFull — together they form the round-trip
// for thlibo install / uninstall. See THREAT_MODEL.md finding #16.
func RemoveHooks(settingsPath string) error {
	buf, err := os.ReadFile(settingsPath) // #nosec G304 -- same rationale as MergeSettingsFull
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("claudecode: read %s: %w", settingsPath, err)
	}
	if len(buf) == 0 {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(buf, &root); err != nil {
		return fmt.Errorf("claudecode: parse %s: %w", settingsPath, err)
	}

	if removePreToolUseHooks(root) {
		encoded, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return fmt.Errorf("claudecode: marshal settings: %w", err)
		}
		if err := os.WriteFile(settingsPath, encoded, 0o600); err != nil {
			return fmt.Errorf("claudecode: write %s: %w", settingsPath, err)
		}
	}
	return nil
}

// removePreToolUseHooks mutates root, returning true if any entry
// was removed. Any entry whose command string contains either the
// .sh or .ps1 marker suffix is dropped; matcher groups whose hooks
// array goes empty are also dropped; an empty PreToolUse array is
// deleted entirely.
func removePreToolUseHooks(root map[string]any) bool {
	hooksObj, ok := root["hooks"].(map[string]any)
	if !ok {
		return false
	}
	preArr, ok := hooksObj["PreToolUse"].([]any)
	if !ok {
		return false
	}
	var changed bool
	outGroups := preArr[:0]
	for _, g := range preArr {
		obj, ok := g.(map[string]any)
		if !ok {
			outGroups = append(outGroups, g)
			continue
		}
		hooksList, _ := obj["hooks"].([]any)
		keep := hooksList[:0]
		for _, h := range hooksList {
			hobj, ok := h.(map[string]any)
			if !ok {
				keep = append(keep, h)
				continue
			}
			cmd, _ := hobj["command"].(string)
			n := normalisePath(cmd)
			if strings.Contains(n, hookMarker) || strings.Contains(n, hookMarkerPS1) {
				changed = true
				continue // drop
			}
			keep = append(keep, h)
		}
		if len(keep) == 0 {
			changed = true
			continue // drop empty group
		}
		obj["hooks"] = keep
		outGroups = append(outGroups, obj)
	}
	if len(outGroups) == 0 {
		delete(hooksObj, "PreToolUse")
		if len(hooksObj) == 0 {
			delete(root, "hooks")
		}
	} else {
		hooksObj["PreToolUse"] = outGroups
	}
	return changed
}

// addPreToolUseHook mutates root in-place. It walks/creates the
// nested structure hooks.PreToolUse[?matcher==<matcher>].hooks[] and
// appends our command entry. If an entry for our hook already
// exists (recognised by markerSuffix in the command string) it's
// updated in place instead of duplicated.
//
// Windows note: the command string is normalised to forward slashes
// so that when Claude Code's Bash tool spawns bash -c "<cmd>", bash
// doesn't interpret backslashes as shell escapes. Git Bash / MSYS
// handle `C:/path/to/file` correctly.
func addPreToolUseHook(root map[string]any, matcher, hookPath, markerSuffix string) {
	cmdString := buildHookCommand(matcher, hookPath)

	hooks := asObject(root, "hooks")
	preArr := asArray(hooks, "PreToolUse")

	// Find an existing matcher group.
	var group map[string]any
	for _, g := range preArr.items() {
		obj, ok := g.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := obj["matcher"].(string); s == matcher {
			group = obj
			break
		}
	}
	if group == nil {
		group = map[string]any{"matcher": matcher, "hooks": []any{}}
		preArr.append(group)
	}
	groupHooks, _ := group["hooks"].([]any)
	if groupHooks == nil {
		groupHooks = []any{}
	}

	// Look for our existing entry. Recognise by marker suffix so a
	// rename of the script (e.g. user moved it to a shared dir)
	// still updates the same slot.
	for i, h := range groupHooks {
		obj, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := obj["command"].(string)
		// Normalise the stored command too so a legacy \-path entry
		// written by an older thlibo version gets upgraded in place
		// rather than left alongside a new /-path entry.
		if strings.Contains(normalisePath(cmd), markerSuffix) {
			groupHooks[i] = map[string]any{"type": "command", "command": cmdString}
			group["hooks"] = groupHooks
			return
		}
	}

	groupHooks = append(groupHooks, map[string]any{"type": "command", "command": cmdString})
	group["hooks"] = groupHooks
}

// buildHookCommand returns the `command` string for a PreToolUse
// entry. Bash hooks are invoked as the raw script path (Claude Code
// runs them via `bash -c`). PowerShell hooks are invoked via
// `powershell -ExecutionPolicy Bypass -File <path>` so systems where
// the signed-script policy would block direct execution still work.
func buildHookCommand(matcher, hookPath string) string {
	hookPath = normalisePath(hookPath)
	if matcher == "PowerShell" {
		return `powershell -NoProfile -ExecutionPolicy Bypass -File "` + hookPath + `"`
	}
	return hookPath
}

// asObject returns root[key] as a map, creating it if absent or if
// the existing value is the wrong type (invariant: we never lose
// data silently — a wrong type suggests settings corruption a human
// needs to look at, but since this helper is only used for our own
// keys we're comfortable replacing).
func asObject(root map[string]any, key string) map[string]any {
	if existing, ok := root[key].(map[string]any); ok {
		return existing
	}
	fresh := map[string]any{}
	root[key] = fresh
	return fresh
}

// arr wraps a slice in a struct so functions can mutate it via a
// pointer-like handle without exposing the parent map.
type arr struct {
	owner map[string]any
	key   string
}

func asArray(root map[string]any, key string) *arr {
	a := &arr{owner: root, key: key}
	if _, ok := root[key].([]any); !ok {
		root[key] = []any{}
	}
	return a
}

func (a *arr) items() []any {
	v, _ := a.owner[a.key].([]any)
	return v
}

func (a *arr) append(x any) {
	v, _ := a.owner[a.key].([]any)
	a.owner[a.key] = append(v, x)
}

// normalisePath converts a Windows-style path to forward slashes.
// On non-Windows, it's a no-op. We don't rewrite the drive letter;
// Git Bash accepts both `C:/...` and `/c/...`, and Claude Code's
// Bash tool resolves `C:/...` correctly.
func normalisePath(p string) string {
	// Simple, allocation-free for the common case where no change
	// is needed.
	if !strings.ContainsRune(p, '\\') {
		return p
	}
	return strings.ReplaceAll(p, "\\", "/")
}
