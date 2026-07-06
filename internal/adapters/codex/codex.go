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
//	2. An inline [[hooks.PostToolUse]] (matcher "^Bash$") block appended
//	   to ~/.codex/config.toml pointing at the hook (#170). Written
//	   inline — not to a separate hooks.json — because Codex warns and
//	   degrades when one config layer mixes hooks.json + inline tables,
//	   and other tools (git-ai/taco) already write inline, so the hook
//	   would not reliably surface in `/hooks`.
//	3. The [features] hooks = true flag in ~/.codex/config.toml, without
//	   which Codex ignores all hooks.
package codex

import (
	_ "embed"
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
// previously-installed thlibo entry (so a reinstall is idempotent).
const hookMarker = "thlibo-rewrite-codex.sh"

// MergeConfigTOMLHook appends thlibo's PostToolUse/^Bash$ hook INLINE
// into the user's config.toml, if not already present.
//
// Why inline config.toml and not a separate hooks.json (#170): when a
// single config layer (~/.codex) contains BOTH a hooks.json and inline
// [[hooks.*]] tables, Codex warns ("loading hooks from both … prefer a
// single representation for this layer") and thlibo's hooks.json hook
// doesn't reliably surface in `/hooks`. Tools like git-ai / taco write
// inline config.toml hooks, so on a real machine that mixed state is
// the norm. Writing thlibo's hook in the SAME representation the rest of
// the layer uses avoids the split entirely.
//
// The appended block (Codex canonical inline shape,
// developers.openai.com/codex/hooks) is:
//
//	[[hooks.PostToolUse]]
//	matcher = "^Bash$"
//
//	[[hooks.PostToolUse.hooks]]
//	type = "command"
//	command = '<hook path>'
//
// Multiple [[hooks.PostToolUse]] array-of-tables are additive, so
// appending never disturbs git-ai/taco's own blocks. Idempotent: if a
// thlibo hook command is already present anywhere in the file, this is a
// no-op. Non-destructive: we only ever append; existing content is
// untouched.
func MergeConfigTOMLHook(configPath, hookPath string) error {
	hookPath = normalisePath(hookPath)

	existing, err := readFileOrEmpty(configPath)
	if err != nil {
		return err
	}

	// Idempotent: a prior thlibo hook (recognised by the script marker)
	// means nothing to do — leave the file byte-for-byte unchanged.
	if strings.Contains(normalisePath(existing), hookMarker) {
		return nil
	}

	// TOML single-quoted (literal) string: no escapes are processed, so
	// a Windows path's backslashes are safe. A literal string cannot
	// contain a single quote; our install paths never do, but guard by
	// falling back to a double-quoted string with backslashes escaped if
	// one somehow appears.
	var cmdLit string
	if strings.Contains(hookPath, "'") {
		cmdLit = `"` + strings.ReplaceAll(hookPath, `\`, `\\`) + `"`
	} else {
		cmdLit = "'" + hookPath + "'"
	}

	block := "\n[[hooks.PostToolUse]]\nmatcher = \"^Bash$\"\n\n[[hooks.PostToolUse.hooks]]\ntype = \"command\"\ncommand = " + cmdLit + "\n"

	// Ensure a newline boundary before the appended block so we don't
	// glue onto a trailing partial line.
	out := existing
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += block

	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return fmt.Errorf("codex: create config dir: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(out), 0o600); err != nil {
		return fmt.Errorf("codex: write %s: %w", configPath, err)
	}
	return nil
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
				// in place, preserving the line's original indentation
				// (and which key name the user already had).
				indent := lines[j][:len(lines[j])-len(strings.TrimLeft(lines[j], " \t"))]
				lines[j] = indent + key + " = true"
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
// line sets the value to true. A trailing TOML comment (`# ...`) is
// ignored, so `hooks = true # enabled by git-ai` still reads as true
// (otherwise we'd needlessly rewrite the line and strip the comment).
func hooksFlagEnabled(line string) bool {
	_, after, found := strings.Cut(line, "=")
	if !found {
		return false
	}
	if idx := strings.IndexByte(after, '#'); idx >= 0 {
		after = after[:idx]
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
