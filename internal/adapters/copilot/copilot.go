// Package copilot installs thlibo's hooks into the GitHub Copilot CLI.
//
// Copilot CLI hooks are external commands registered in a JSON file
// under ~/.copilot/hooks/ (docs.github.com/copilot/concepts/agents/hooks
// and .../reference/hooks-reference). Unlike Codex (inline config.toml)
// and Cursor (one shared hooks.json), Copilot reads EVERY *.json in that
// directory and each tool owns its own file — the git-ai tool, for
// instance, ships ~/.copilot/hooks/git-ai.json. So thlibo writes its own
// ~/.copilot/hooks/thlibo.json and never has to merge into, or risk
// clobbering, another tool's file. Uninstall just deletes it.
//
// thlibo installs BOTH hook events Copilot offers for interception:
//
//   - preToolUse  — rewrites a shell command's INPUT via `modifiedArgs`
//     (`git status` → `<thlibo> exec -- git status`), the same
//     command-wrap path used for Claude Code's Bash tool. Copilot's
//     preToolUse is FAIL-CLOSED (a hook error denies the tool call), so
//     the hook scripts exit 0 on every path and only ever "allow".
//   - postToolUse — replaces a tool's OUTPUT via `modifiedResult`,
//     piping toolResult.textResultForLlm through `thlibo compress`, the
//     same mechanism as Codex's decision:block. postToolUse is
//     fail-open. A double-compression guard in the postToolUse hook
//     skips output whose command was already wrapped by preToolUse.
//
// The Copilot hook schema carries BOTH a "bash" and a "powershell"
// command per entry, so thlibo ships a native .sh and .ps1 for each
// event (the claudecode precedent). Windows runs the .ps1 directly —
// no Git-Bash wrapping, unlike the Cursor adapter.
package copilot

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed hook-pre.sh
var preHookSh []byte

//go:embed hook-pre.ps1
var preHookPS1 []byte

//go:embed hook-post.sh
var postHookSh []byte

//go:embed hook-post.ps1
var postHookPS1 []byte

// Embedded-script accessors (used by tests and the installer).
func PreHookSh() []byte   { return preHookSh }
func PreHookPS1() []byte  { return preHookPS1 }
func PostHookSh() []byte  { return postHookSh }
func PostHookPS1() []byte { return postHookPS1 }

// Hook-script filenames written into the thlibo hook dir. These names
// also serve as the markers that recognise a prior install.
const (
	PreHookShName   = "thlibo-pre-copilot.sh"
	PreHookPS1Name  = "thlibo-pre-copilot.ps1"
	PostHookShName  = "thlibo-post-copilot.sh"
	PostHookPS1Name = "thlibo-post-copilot.ps1"
)

// HookTimeoutSec bounds each hook invocation. A slow compress must not
// hang Copilot; preToolUse timeout is fail-open per the docs, and
// postToolUse is fail-open on every error, so a timeout is safe.
const HookTimeoutSec = 30

// WriteHookScripts writes all four hook scripts into hookDir (0o700).
func WriteHookScripts(hookDir string) error {
	scripts := []struct {
		name string
		body []byte
	}{
		{PreHookShName, preHookSh},
		{PreHookPS1Name, preHookPS1},
		{PostHookShName, postHookSh},
		{PostHookPS1Name, postHookPS1},
	}
	if err := os.MkdirAll(hookDir, 0o750); err != nil {
		return fmt.Errorf("copilot: create hook dir: %w", err)
	}
	for _, s := range scripts {
		path := filepath.Join(hookDir, s.name)
		if err := os.WriteFile(path, s.body, 0o600); err != nil {
			return fmt.Errorf("copilot: write %s: %w", s.name, err)
		}
		// #nosec G302 -- owner-execute required; group/other remain 0.
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("copilot: chmod %s: %w", s.name, err)
		}
	}
	return nil
}

// WriteHooksJSON writes ~/.copilot/hooks/thlibo.json registering the
// preToolUse + postToolUse hooks. Because Copilot reads each tool's file
// independently, thlibo owns this file outright: we write the canonical
// document every install (idempotent — same input, same bytes), which
// also self-heals a hand-edited or stale file. hookDir is where the four
// scripts live (see WriteHookScripts).
//
// The Copilot hook file schema
// (docs.github.com/copilot/concepts/agents/hooks):
//
//	{
//	  "version": 1,
//	  "hooks": {
//	    "preToolUse":  [ { "type":"command", "matcher":"...",
//	                       "bash":"<pre.sh>", "powershell":"<pre.ps1>",
//	                       "timeoutSec":30 } ],
//	    "postToolUse": [ { "type":"command",
//	                       "bash":"<post.sh>", "powershell":"<post.ps1>",
//	                       "timeoutSec":30 } ]
//	  }
//	}
//
// preToolUse carries a matcher so command-rewriting only fires on the
// shell tool (bash/shell/powershell); postToolUse omits the matcher so
// it can compress the output of any verbose tool (the hook self-filters
// on size and shape).
func WriteHooksJSON(hooksJSONPath, hookDir string) error {
	pre := hookEntry(
		filepath.Join(hookDir, PreHookShName),
		filepath.Join(hookDir, PreHookPS1Name),
		shellMatcher,
	)
	post := hookEntry(
		filepath.Join(hookDir, PostHookShName),
		filepath.Join(hookDir, PostHookPS1Name),
		"", // no matcher: compress any tool's output
	)

	doc := map[string]any{
		"version": 1,
		"hooks": map[string]any{
			"preToolUse":  []any{pre},
			"postToolUse": []any{post},
		},
	}

	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("copilot: marshal hooks: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := os.MkdirAll(filepath.Dir(hooksJSONPath), 0o750); err != nil {
		return fmt.Errorf("copilot: create hooks dir: %w", err)
	}
	if err := os.WriteFile(hooksJSONPath, encoded, 0o600); err != nil {
		return fmt.Errorf("copilot: write %s: %w", hooksJSONPath, err)
	}
	return nil
}

// shellMatcher is the regex Copilot compiles as ^(?:PATTERN)$ against
// toolName. Copilot names the shell tool "bash" on Unix and "powershell"
// on Windows; "shell" is included defensively.
const shellMatcher = "bash|shell|powershell"

// hookEntry builds one Copilot hook-file entry. matcher is omitted when
// empty so the hook applies to every tool.
func hookEntry(bashPath, ps1Path, matcher string) map[string]any {
	e := map[string]any{
		"type":       "command",
		"bash":       normalisePath(bashPath),
		"powershell": normalisePath(ps1Path),
		"timeoutSec": HookTimeoutSec,
	}
	if matcher != "" {
		e["matcher"] = matcher
	}
	return e
}

// RemoveHooks deletes thlibo's Copilot hook file and the four hook
// scripts. A no-op (nil) when nothing is installed. Only thlibo's own
// file is removed; other tools' hook files are never touched.
func RemoveHooks(hooksJSONPath, hookDir string) error {
	var firstErr error
	remove := func(p string) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	remove(hooksJSONPath)
	for _, name := range []string{PreHookShName, PreHookPS1Name, PostHookShName, PostHookPS1Name} {
		remove(filepath.Join(hookDir, name))
	}
	if firstErr != nil {
		return fmt.Errorf("copilot: remove hooks: %w", firstErr)
	}
	return nil
}

// normalisePath converts backslashes to forward slashes. The "bash"
// command runs the script through bash, which eats backslashes as
// escapes; forward slashes are valid for both bash and PowerShell on
// Windows. Same fix the other adapters apply to hook paths.
func normalisePath(p string) string {
	if !strings.ContainsRune(p, '\\') {
		return p
	}
	return strings.ReplaceAll(p, "\\", "/")
}
