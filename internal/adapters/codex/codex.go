// Package codex installs thlibo's PostToolUse hook into Codex CLI's
// configuration.
//
// Per the Codex hooks reference (developers.openai.com/codex/hooks),
// PostToolUse supports `decision: "block"` with a `reason` field
// that *replaces* the tool result the model sees:
//
//   "Codex records the feedback, replaces the tool result with that
//    feedback, and continues the model from the hook-provided message."
//
// That is the real compression path on Codex — different mechanism
// from Claude Code's PreToolUse-updatedInput approach but same
// observable effect: the model sees compressed output instead of raw.
//
// The package installs three things:
//
//	1. The hook script (thlibo-rewrite-codex.sh) to the user's hook dir.
//	2. A hooks.json entry: PostToolUse/^Bash$/command→<hook>, merged
//	   into ~/.codex/hooks.json without clobbering other events.
//	3. The [features] codex_hooks = true flag in ~/.codex/config.toml,
//	   without which Codex silently ignores all hooks.
package codex

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

// HookScript returns the embedded Codex hook script bytes.
func HookScript() []byte { return hookScript }

// WriteHookScript writes the hook script to path (0o700, owner-only).
func WriteHookScript(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("codex: create hook dir: %w", err)
	}
	if err := os.WriteFile(path, hookScript, 0o600); err != nil {
		return fmt.Errorf("codex: write hook: %w", err)
	}
	// #nosec G302 -- owner-execute required; group/other remain 0.
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("codex: chmod hook: %w", err)
	}
	return nil
}

// hookMarker is the filename substring we use to recognise a
// previously-installed thlibo entry and update it in place.
const hookMarker = "thlibo-rewrite-codex.sh"

// MergeHooksJSON loads hooksPath, adds a PostToolUse/^Bash$ entry
// pointing at hookPath, and writes back. Every unrelated key and
// every other hook entry is preserved.
//
// The Codex hooks.json schema wraps events under a top-level "hooks"
// object, per the docs example:
//
//	{ "hooks": { "PostToolUse": [ ... ] } }
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
				return fmt.Errorf("codex: parse %s: %w", hooksPath, err)
			}
		}
	case os.IsNotExist(err):
		root = map[string]any{}
	default:
		return fmt.Errorf("codex: read %s: %w", hooksPath, err)
	}

	addPostToolUseHook(root, hookPath)

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("codex: marshal hooks: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return fmt.Errorf("codex: create hooks dir: %w", err)
	}
	if err := os.WriteFile(hooksPath, encoded, 0o600); err != nil {
		return fmt.Errorf("codex: write %s: %w", hooksPath, err)
	}
	return nil
}

// addPostToolUseHook walks/creates hooks.PostToolUse[?matcher==^Bash$]
// .hooks[] and appends (or updates in-place) the thlibo entry.
func addPostToolUseHook(root map[string]any, hookPath string) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}

	post, _ := hooks["PostToolUse"].([]any)
	if post == nil {
		post = []any{}
	}

	var group map[string]any
	for _, g := range post {
		obj, ok := g.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := obj["matcher"].(string); s == "^Bash$" {
			group = obj
			break
		}
	}
	if group == nil {
		group = map[string]any{"matcher": "^Bash$", "hooks": []any{}}
		post = append(post, group)
	}

	groupHooks, _ := group["hooks"].([]any)
	for i, h := range groupHooks {
		obj, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := obj["command"].(string)
		if strings.Contains(normalisePath(cmd), hookMarker) {
			groupHooks[i] = map[string]any{"type": "command", "command": hookPath}
			group["hooks"] = groupHooks
			hooks["PostToolUse"] = post
			return
		}
	}
	groupHooks = append(groupHooks, map[string]any{"type": "command", "command": hookPath})
	group["hooks"] = groupHooks
	hooks["PostToolUse"] = post
}

// EnableHooksFeatureFlag ensures the Codex hooks feature flag is on in
// the user's config.toml. Without it, Codex silently ignores every hook
// it finds.
//
// The canonical key is `[features] hooks = true`. Older Codex used
// `codex_hooks = true`, which still works as a deprecated alias. We
// write the canonical `hooks = true`, but treat an existing
// `hooks = true` OR `codex_hooks = true` as already-satisfied — so we
// never duplicate the flag or fight a config that git-ai (or another
// tool) already enabled.
//
// The TOML here is written by hand rather than through a library:
// hooks.json is the authoritative hook declaration, config.toml
// only needs the one feature flag. Doing it inline keeps the
// dependency footprint small.
//
// Merge semantics: if the file doesn't exist, it's created with
// just the feature flag. If [features] already exists, the flag
// is added/updated without touching any other feature. If the file
// is non-TOML gibberish, we refuse to write so user data isn't lost.
func EnableHooksFeatureFlag(configPath string) error {
	existing, err := readFileOrEmpty(configPath)
	if err != nil {
		return err
	}

	updated, changed, err := ensureCodexHooksTrue(existing)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return fmt.Errorf("codex: create config dir: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		return fmt.Errorf("codex: write %s: %w", configPath, err)
	}
	return nil
}

func readFileOrEmpty(p string) (string, error) {
	buf, err := os.ReadFile(p) // #nosec G304 -- p is chosen by the installer.
	switch {
	case err == nil:
		return string(buf), nil
	case os.IsNotExist(err):
		return "", nil
	default:
		return "", fmt.Errorf("codex: read %s: %w", p, err)
	}
}

// ensureCodexHooksTrue parses a minimal subset of TOML: it finds
// (or creates) the [features] section header and ensures the hooks
// feature flag is enabled inside it. Everything else in the file is
// untouched.
//
// The canonical flag is `hooks = true`. `codex_hooks = true` is a
// deprecated-but-honoured alias. To avoid duplicating the flag (or
// flipping a working config), we treat EITHER key already being `true`
// as satisfied and make no change. Otherwise we write `hooks = true`.
//
// Returns (newContent, changed, err). changed=false means the file
// already had hooks enabled (under either key).
//
// We intentionally do NOT use a full TOML parser. The goal is:
// non-destructive merge for the one feature flag we care about.
// A user's comments, ordering, and other keys survive verbatim.
func ensureCodexHooksTrue(content string) (string, bool, error) {
	lines := strings.Split(content, "\n")

	// Scan for an existing [features] section.
	featuresStart := -1
	featuresEnd := -1 // exclusive: index of the next section header
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[features]" {
			featuresStart = i
			featuresEnd = len(lines)
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
					featuresEnd = j
					break
				}
			}
			break
		}
	}

	const want = "hooks = true"

	if featuresStart >= 0 {
		// [features] exists — look for the canonical key or the alias.
		// If either is already enabled, we're done (don't duplicate).
		for j := featuresStart + 1; j < featuresEnd; j++ {
			t := strings.TrimSpace(lines[j])
			if key, ok := hooksFlagKey(t); ok {
				if hooksFlagEnabled(t) {
					return content, false, nil
				}
				// Key present but not true — set the canonical key true
				// in place (preserving which key the user already had).
				lines[j] = key + " = true"
				return strings.Join(lines, "\n"), true, nil
			}
		}
		// Neither key present; insert the canonical flag after the header.
		before := lines[:featuresStart+1]
		after := append([]string{want}, lines[featuresStart+1:]...)
		return strings.Join(append(before, after...), "\n"), true, nil
	}

	// No [features] section at all — append one.
	prefix := content
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	if prefix != "" && !strings.HasSuffix(prefix, "\n\n") {
		prefix += "\n"
	}
	return prefix + "[features]\n" + want + "\n", true, nil
}

// hooksFlagKey reports whether a TOML line sets the hooks feature flag
// (canonical `hooks` or deprecated alias `codex_hooks`) and returns the
// bare key name. It matches the assignment, not a substring, so a key
// like `hooks_extra` doesn't trip it.
func hooksFlagKey(line string) (string, bool) {
	for _, key := range []string{"hooks", "codex_hooks"} {
		rest, ok := strings.CutPrefix(line, key)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(rest, "=") {
			return key, true
		}
	}
	return "", false
}

// hooksFlagEnabled reports whether a `hooks`/`codex_hooks` assignment
// line sets the value to true.
func hooksFlagEnabled(line string) bool {
	_, after, found := strings.Cut(line, "=")
	if !found {
		return false
	}
	return strings.TrimSpace(after) == "true"
}

// normalisePath converts backslashes to forward slashes so bash -c
// doesn't eat them on Windows. Same fix the claudecode adapter
// applies to its hook path.
func normalisePath(p string) string {
	if !strings.ContainsRune(p, '\\') {
		return p
	}
	return strings.ReplaceAll(p, "\\", "/")
}
