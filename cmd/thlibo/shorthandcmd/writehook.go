package shorthandcmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/3rg0n/thlibo/internal/config"
)

// RunWriteHook implements the `thlibo shorthand-hook` subcommand.
// The Write/Edit PreToolUse hook scripts hand it the Claude Code
// tool envelope on stdin; we decide whether to rewrite, run the
// shorthand engine, and emit the hookSpecificOutput JSON Claude
// Code expects.
//
// Flow:
//
//  1. Read tool envelope from stdin.
//  2. Bail if config has auto_shorthand_on_write=false.
//  3. Extract tool_input.file_path; bail if path doesn't match
//     any configured glob.
//  4. Extract tool_input.content; bail if under min_bytes.
//  5. Run shorthand.Engine.Compress.
//  6. If eval failed -> exit 0 with no output; Claude Code keeps
//     the original tool_input. Same fail-closed contract as the
//     CLI.
//  7. Save the original under ~/.thlibo/cases/shorthand/<sha>-<ts>/
//     so `thlibo shorthand --restore` can recover it.
//  8. Emit hookSpecificOutput JSON with updatedInput.content set
//     to the compressed text.
//
// The hook script that drives this is intentionally tiny: read
// stdin, pipe to `thlibo shorthand-hook`, write its stdout back
// to Claude Code. No JSON parsing in shell.
func RunWriteHook(argv []string) int {
	_ = argv

	// Kill switch — same env gate every other thlibo hook honours.
	if disabled := os.Getenv("THLIBO_DISABLED"); disabled == "1" || disabled == "true" || disabled == "yes" || disabled == "on" {
		return ExitOK
	}

	cfg := config.Load()
	if !cfg.AutoShorthandOnWrite {
		// Feature off → never rewrite. Claude Code reads our empty
		// stdout and keeps the original tool_input.
		return ExitOK
	}

	envBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		// Can't even read input — fail closed, original goes through.
		return ExitOK
	}

	var env struct {
		ToolInput struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
			NewStr   string `json:"new_string"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(envBytes, &env); err != nil {
		return ExitOK
	}

	// Edit tool uses new_string; Write uses content. Apply to
	// whichever has bytes. If both are empty there's nothing to do.
	field, body := pickContent(&env.ToolInput)
	if field == "" {
		return ExitOK
	}

	if env.ToolInput.FilePath == "" {
		return ExitOK
	}
	if !cfg.MatchesAutoShorthandPath(env.ToolInput.FilePath) {
		return ExitOK
	}
	if len(body) < cfg.AutoShorthandMinBytes {
		return ExitOK
	}

	engine, err := buildEngine()
	if err != nil {
		// Daemon unavailable — fail closed.
		return ExitOK
	}

	res, err := engine.Compress(context.Background(), body)
	if err != nil || !res.Safe() || res.AlreadyShorthand {
		// Any of: backend failed, eval failed, or input was already
		// shorthand. In every case we leave the user's bytes alone.
		return ExitOK
	}

	// Save the original somewhere recoverable. ~/.thlibo/cases/shorthand/
	// keys by hash so multiple writes of the same file deduplicate
	// naturally; the timestamp suffix prevents one rewrite from
	// shadowing a previous original of a different version.
	if err := saveOriginal(env.ToolInput.FilePath, body); err != nil {
		// Saving the backup is best-effort; if we can't store the
		// original we still rewrite, but log it. Keeping the new
		// content out of the file entirely would be worse — the
		// user already has the daemon running, the eval already
		// passed.
		fmt.Fprintln(os.Stderr, "thlibo shorthand-hook: backup save failed:", err)
	}

	// Emit hookSpecificOutput with updatedInput. Claude Code reads
	// this and substitutes the rewritten content into the tool call
	// before the file write happens.
	out, err := buildHookOutput(envBytes, field, res.Compressed)
	if err != nil {
		return ExitOK
	}
	if _, err := os.Stdout.Write(out); err != nil {
		return ExitOK
	}
	return ExitOK
}

// pickContent returns whichever of (content, new_string) has
// bytes. Write tool uses content; Edit tool uses new_string for
// its replacement. Returns (fieldName, value) or ("", "") if
// neither is populated.
func pickContent(ti *struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
	NewStr   string `json:"new_string"`
}) (string, string) {
	if ti.Content != "" {
		return "content", ti.Content
	}
	if ti.NewStr != "" {
		return "new_string", ti.NewStr
	}
	return "", ""
}

// buildHookOutput rebuilds the envelope's tool_input with the new
// content for the chosen field, then wraps it in
// hookSpecificOutput. We use generic decoding so we preserve every
// field Claude Code sent — we only mutate the one we're rewriting.
func buildHookOutput(envBytes []byte, field, newContent string) ([]byte, error) {
	var generic map[string]any
	if err := json.Unmarshal(envBytes, &generic); err != nil {
		return nil, err
	}
	ti, ok := generic["tool_input"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool_input missing")
	}
	ti[field] = newContent

	resp := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "allow",
			"permissionDecisionReason": "thlibo shorthand auto-rewrite",
			"updatedInput":             ti,
		},
	}
	return json.Marshal(resp)
}

// saveOriginal writes the pre-shorthand bytes under
// ~/.thlibo/cases/shorthand/<sha-prefix>-<unix-ts>/original so
// the user can `thlibo shorthand --restore <file>` if anything
// looks wrong after the auto-rewrite.
func saveOriginal(targetPath, body string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".thlibo", "cases", "shorthand")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}

	sum := sha256.Sum256([]byte(body))
	prefix := hex.EncodeToString(sum[:8])
	dir := filepath.Join(root, fmt.Sprintf("%s-%d", prefix, time.Now().UTC().Unix()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, "original"), []byte(body), 0o600); err != nil {
		return err
	}

	// meta.json — what file this came from, when, what hash.
	meta := map[string]any{
		"target_path": targetPath,
		"saved_utc":   time.Now().UTC().Format(time.RFC3339),
		"sha256":      hex.EncodeToString(sum[:]),
		"size_bytes":  len(body),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o600)
}
