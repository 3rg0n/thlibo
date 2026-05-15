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
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
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

//go:embed hook-read.sh
var hookReadScript []byte

//go:embed hook-read.ps1
var hookReadScriptPS1 []byte

//go:embed hook-write.sh
var hookWriteScript []byte

//go:embed hook-write.ps1
var hookWriteScriptPS1 []byte

// HookScript returns the embedded Bash Exec-hook script bytes,
// unmodified. Exposed so the installer can write it to disk and
// tests can assert on its shape without running an install.
func HookScript() []byte { return hookScript }

// HookScriptPS1 returns the embedded PowerShell Exec-hook script
// bytes, unmodified. Companion to HookScript for Windows installs
// where CLAUDE_CODE_USE_POWERSHELL_TOOL=1 routes tool calls through
// the PowerShell tool instead of Bash.
func HookScriptPS1() []byte { return hookScriptPS1 }

// HookReadScript returns the embedded Bash PreToolUse hook for
// the Read tool. Fired when Claude Code reads a large log-shaped
// file; rewrites tool_input.file_path to a compressed case variant.
func HookReadScript() []byte { return hookReadScript }

// HookReadScriptPS1 returns the PowerShell equivalent for the Read
// hook. Same semantics as HookReadScript.
func HookReadScriptPS1() []byte { return hookReadScriptPS1 }

// WriteResult is returned by WriteHookScript and WriteHookScriptPS1.
type WriteResult int

const (
	// WriteResultCreated means the file did not exist and was written fresh.
	WriteResultCreated WriteResult = iota
	// WriteResultUpdated means the file existed unchanged and was updated.
	WriteResultUpdated
	// WriteResultUnchanged means the installed version matches embedded; no write needed.
	WriteResultUnchanged
	// WriteResultConflict means the user modified the file; the new version
	// was written to <path>.new and the original was not touched.
	WriteResultConflict
)

func (r WriteResult) String() string {
	switch r {
	case WriteResultCreated:
		return "created"
	case WriteResultUpdated:
		return "updated"
	case WriteResultUnchanged:
		return "unchanged"
	case WriteResultConflict:
		return "conflict"
	}
	return "unknown"
}

// WriteHookScript writes the Bash hook script to path.
// Returns WriteResultConflict (and writes to path+".new") if the user
// has modified the installed file since the last thlibo install.
func WriteHookScript(path string) (WriteResult, error) {
	return writeHookBytes(path, hookScript, "# thlibo-installed-sha: ")
}

// WriteHookScriptPS1 writes the PowerShell hook script to path.
// Same conflict semantics as WriteHookScript.
func WriteHookScriptPS1(path string) (WriteResult, error) {
	return writeHookBytes(path, hookScriptPS1, "# thlibo-installed-sha: ")
}

// WriteHookReadScript writes the Bash Read-tool hook to path, using
// the same write-if-new / conflict-to-.new semantics as the Exec hook.
func WriteHookReadScript(path string) (WriteResult, error) {
	return writeHookBytes(path, hookReadScript, "# thlibo-installed-sha: ")
}

// WriteHookReadScriptPS1 writes the PowerShell Read-tool hook.
func WriteHookReadScriptPS1(path string) (WriteResult, error) {
	return writeHookBytes(path, hookReadScriptPS1, "# thlibo-installed-sha: ")
}

// WriteHookWriteScript writes the Bash Write+Edit-tool hook to
// path, using the same write-if-new / conflict-to-.new semantics
// as every other hook script.
func WriteHookWriteScript(path string) (WriteResult, error) {
	return writeHookBytes(path, hookWriteScript, "# thlibo-installed-sha: ")
}

// WriteHookWriteScriptPS1 writes the PowerShell Write+Edit-tool hook.
func WriteHookWriteScriptPS1(path string) (WriteResult, error) {
	return writeHookBytes(path, hookWriteScriptPS1, "# thlibo-installed-sha: ")
}

// hookContentHash returns the SHA-256 of b, hex-encoded.
func hookContentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// stampedContent returns b with a SHA-256 comment appended on the
// second line (after the shebang / first line) so the installed file
// is self-describing. The stamp is computed over the original b so
// repeated installs produce stable content.
func stampedContent(b []byte, prefix string) []byte {
	hash := hookContentHash(b)
	stamp := []byte(prefix + hash + "\n")

	// Insert after the first line (shebang or first comment).
	nl := bytes.IndexByte(b, '\n')
	if nl < 0 {
		return append(b, append([]byte("\n"), stamp...)...)
	}
	out := make([]byte, 0, len(b)+len(stamp))
	out = append(out, b[:nl+1]...)
	out = append(out, stamp...)
	out = append(out, b[nl+1:]...)
	return out
}

// extractInstalledHash reads the stamp comment from an on-disk hook
// file and returns the stored hash. Returns "" if not found.
func extractInstalledHash(data []byte, prefix string) string {
	for _, line := range bytes.Split(data, []byte("\n")) {
		s := string(line)
		if strings.HasPrefix(s, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(s, prefix))
		}
	}
	return ""
}

// writeHookBytes is the shared implementation. It stamps the embedded
// content with a SHA comment, then:
//
//   - File absent → write stamped content (created).
//   - File present, installed hash matches embedded hash → no-op (unchanged).
//   - File present, installed hash matches stored hash in file → overwrite
//     with new stamped content (updated — user never edited it).
//   - File present, stored hash != current file hash → user edited it;
//     write new version to path+".new" and return conflict without
//     touching the user's file.
func writeHookBytes(path string, b []byte, prefix string) (WriteResult, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return WriteResultCreated, fmt.Errorf("claudecode: create hook dir: %w", err)
	}

	embeddedHash := hookContentHash(b)
	stamped := stampedContent(b, prefix)

	existing, err := os.ReadFile(path) // #nosec G304 -- path is installer-derived
	if err != nil {
		if !os.IsNotExist(err) {
			return WriteResultCreated, fmt.Errorf("claudecode: read existing hook: %w", err)
		}
		// File doesn't exist — write fresh.
		return WriteResultCreated, commitHookFile(path, stamped)
	}

	// File exists. Check whether the embedded content changed at all.
	storedHash := extractInstalledHash(existing, prefix)
	if storedHash == embeddedHash {
		return WriteResultUnchanged, nil // already up to date
	}

	// Legacy install: no stamp in the file at all. Treat as pristine if
	// the on-disk bytes match the embedded content exactly — this happens
	// when an older thlibo version wrote the hook without stamping it.
	if storedHash == "" && bytes.Equal(existing, b) {
		return WriteResultUpdated, commitHookFile(path, stamped)
	}

	// Embedded version is newer (or different). Check whether the user
	// modified the on-disk file since it was installed. We detect this
	// by removing the stamp line from the on-disk file and hashing the
	// remainder — if it equals storedHash, the file is pristine.
	withoutStamp := removeStampLine(existing, prefix)
	currentHash := hookContentHash(withoutStamp)
	if currentHash != storedHash {
		// User edited the file — write new version alongside and warn.
		newPath := path + ".new"
		if err := commitHookFile(newPath, stamped); err != nil {
			return WriteResultConflict, fmt.Errorf("claudecode: write %s: %w", newPath, err)
		}
		return WriteResultConflict, nil
	}

	// File is pristine (user never edited it) — safe to overwrite.
	return WriteResultUpdated, commitHookFile(path, stamped)
}

// removeStampLine strips the stamp comment line from b so we can hash
// the content without the stamp to compare against the stored hash.
func removeStampLine(b []byte, prefix string) []byte {
	var out []byte
	for _, line := range bytes.Split(b, []byte("\n")) {
		if strings.HasPrefix(string(line), prefix) {
			continue
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	// Trim trailing newline added by the loop.
	return bytes.TrimSuffix(out, []byte("\n"))
}

func commitHookFile(path string, b []byte) error {
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("claudecode: write hook: %w", err)
	}
	// #nosec G302 -- owner-execute bit required; group/other stay 0.
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
	hookMarker         = "thlibo-rewrite.sh"
	hookMarkerPS1      = "thlibo-rewrite.ps1"
	hookMarkerRead     = "thlibo-read.sh"
	hookMarkerReadPS1  = "thlibo-read.ps1"
	hookMarkerWrite    = "thlibo-write.sh"
	hookMarkerWritePS1 = "thlibo-write.ps1"
)

// allHookMarkers is the set of filename suffixes that identify a
// hook entry as "ours" for removal + shadow-detection purposes.
func allHookMarkers() []string {
	return []string{
		hookMarker, hookMarkerPS1,
		hookMarkerRead, hookMarkerReadPS1,
		hookMarkerWrite, hookMarkerWritePS1,
	}
}

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

// MergeSettingsFull is the two-hook form: Bash + PowerShell
// Exec-tool matchers. Kept so callers already using it don't break.
// See MergeSettingsWithRead for the full four-matcher version that
// also installs the Read-tool hooks.
func MergeSettingsFull(settingsPath, bashHookPath, ps1HookPath string) error {
	return MergeSettingsWithRead(settingsPath,
		bashHookPath, ps1HookPath, "", "")
}

// MergeSettingsWithRead loads settingsPath (creating an empty object
// if the file doesn't exist), adds a PreToolUse hook entry for each
// non-empty hook path, and writes the file back.
//
// Kept as a thin wrapper around MergeSettingsAll for back-compat.
// New callers should use MergeSettingsAll directly.
func MergeSettingsWithRead(settingsPath, bashHookPath, ps1HookPath, readHookPath, readPS1HookPath string) error {
	return MergeSettingsAll(settingsPath, MergeHooks{
		BashExecHook:    bashHookPath,
		PS1ExecHook:     ps1HookPath,
		BashReadHook:    readHookPath,
		PS1ReadHook:     readPS1HookPath,
	})
}

// MergeHooks is the named-arg form for MergeSettingsAll. Empty
// fields skip the corresponding matcher. Read and Write each
// install one entry per host: PowerShell variant on Windows, Bash
// elsewhere, falling back to whichever was supplied.
type MergeHooks struct {
	// Exec-tool hooks (Bash and PowerShell tools — both register).
	BashExecHook string
	PS1ExecHook  string

	// Read-tool hooks (one entry per host).
	BashReadHook string
	PS1ReadHook  string

	// Write-tool hooks (one entry per host, registered against
	// the Write AND Edit matchers because both write to disk).
	BashWriteHook string
	PS1WriteHook  string
}

// MergeSettingsAll loads settingsPath, registers each hook the
// caller provided, and writes the file back. Idempotent across
// reinstalls. Preserves every unrelated key and every unrelated
// hook entry. Failure to parse existing JSON is fatal — we never
// clobber a settings file we can't safely read first.
func MergeSettingsAll(settingsPath string, h MergeHooks) error {
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

	if h.BashExecHook != "" {
		addPreToolUseHook(root, "Bash", h.BashExecHook, hookMarker)
	}
	if h.PS1ExecHook != "" {
		addPreToolUseHook(root, "PowerShell", h.PS1ExecHook, hookMarkerPS1)
	}
	if path, marker := pickPlatformHook(h.BashReadHook, h.PS1ReadHook,
		hookMarkerRead, hookMarkerReadPS1); path != "" {
		addPreToolUseHook(root, "Read", path, marker)
	}
	// Write hooks register against BOTH Write and Edit matchers —
	// both tools land bytes on disk, both should round-trip
	// shorthand the same way. Same physical script, two settings
	// entries (Claude Code matches by tool name).
	if path, marker := pickPlatformHook(h.BashWriteHook, h.PS1WriteHook,
		hookMarkerWrite, hookMarkerWritePS1); path != "" {
		addPreToolUseHook(root, "Write", path, marker)
		addPreToolUseHook(root, "Edit", path, marker)
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

// pickPlatformHook returns whichever of (bashPath, ps1Path) suits
// this host plus the matching marker for cleanup recognition. The
// Windows PowerShell variant wins on Windows, the Bash variant
// wins elsewhere, and a single-supplied path is used regardless.
func pickPlatformHook(bashPath, ps1Path, bashMarker, ps1Marker string) (path, marker string) {
	onWindows := runtimeIsWindows()
	switch {
	case onWindows && ps1Path != "":
		return ps1Path, ps1Marker
	case !onWindows && bashPath != "":
		return bashPath, bashMarker
	case bashPath != "":
		return bashPath, bashMarker
	case ps1Path != "":
		return ps1Path, ps1Marker
	}
	return "", ""
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
			if isThliboHookCommand(n) {
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

// isThliboHookCommand reports whether a (normalised, forward-slash)
// command string points at any of our installed hook scripts —
// Exec (Bash/PS1) or Read (Bash/PS1). Marker membership is a
// substring check so a user-initiated move of the script file still
// identifies it on the next install/uninstall cycle.
func isThliboHookCommand(normalisedCmd string) bool {
	for _, m := range allHookMarkers() {
		if strings.Contains(normalisedCmd, m) {
			return true
		}
	}
	return false
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
