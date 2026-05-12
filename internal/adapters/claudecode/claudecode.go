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

// HookScript returns the embedded hook script bytes, unmodified.
// Exposed so the installer can write it to disk and tests can
// assert on its shape without running an install.
func HookScript() []byte { return hookScript }

// WriteHookScript writes the hook script to path, creating parent
// directories as needed. The file is created with a restrictive
// mode (0o600) and then chmod'd to 0o700 so the owner can execute
// it. Group and world have no access — this is user-scoped tooling.
// On Windows the execute bit has no meaning; the file is still
// written with 0o600-equivalent ACLs through Go's os.WriteFile.
func WriteHookScript(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("claudecode: create hook dir: %w", err)
	}
	// Two-step: WriteFile respects the gosec G306 guidance (0o600),
	// then chmod adds the owner-execute bit needed for the hook to
	// run. Group/other stay at 0 — nothing about this script is
	// meant to be shared.
	if err := os.WriteFile(path, hookScript, 0o600); err != nil {
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

// Our entry marker: a fixed string in the command path that lets
// MergeSettings recognise a previous install so re-running the
// installer doesn't pile up duplicate entries.
const hookMarker = "thlibo-rewrite.sh"

// MergeSettings loads settingsPath (creating an empty object if the
// file doesn't exist), adds a PreToolUse/Bash hook pointing at
// hookPath, and writes the file back. Preserves every other key
// and every other hook entry verbatim.
//
// Idempotent: calling it twice on the same settings file leaves
// exactly one thlibo hook entry. If settingsPath is invalid JSON,
// returns an error without modifying anything.
func MergeSettings(settingsPath, hookPath string) error {
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

	addBashPreToolUseHook(root, hookPath)

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

// addBashPreToolUseHook mutates root in-place. It walks/creates the
// nested structure hooks.PreToolUse[?matcher==Bash].hooks[] and
// appends our command entry. If an entry for our hook already
// exists (recognised by the hookMarker suffix) it's updated in
// place instead of duplicated.
//
// Windows note: the command string is normalised to forward slashes
// so that when Claude Code's Bash tool spawns bash -c "<cmd>", bash
// doesn't interpret backslashes as shell escapes. Git Bash / MSYS
// handle `C:/path/to/file` correctly.
func addBashPreToolUseHook(root map[string]any, hookPath string) {
	hookPath = normalisePath(hookPath)

	hooks := asObject(root, "hooks")
	preArr := asArray(hooks, "PreToolUse")

	// Find an existing matcher group for "Bash".
	var group map[string]any
	for _, g := range preArr.items() {
		obj, ok := g.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := obj["matcher"].(string); s == "Bash" {
			group = obj
			break
		}
	}
	if group == nil {
		group = map[string]any{"matcher": "Bash", "hooks": []any{}}
		preArr.append(group)
	}
	// hooks: []any
	groupHooks, _ := group["hooks"].([]any)
	if groupHooks == nil {
		groupHooks = []any{}
	}

	// Look for our existing entry. Recognise by command-string
	// suffix so a rename of the script (e.g. user moved it to a
	// shared dir) still updates the same slot.
	for i, h := range groupHooks {
		obj, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := obj["command"].(string)
		// Normalise the stored command too so a legacy \-path entry
		// written by an older thlibo version gets upgraded in place
		// rather than left alongside a new /-path entry.
		if strings.Contains(normalisePath(cmd), hookMarker) {
			groupHooks[i] = map[string]any{"type": "command", "command": hookPath}
			group["hooks"] = groupHooks
			return
		}
	}

	groupHooks = append(groupHooks, map[string]any{"type": "command", "command": hookPath})
	group["hooks"] = groupHooks
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
