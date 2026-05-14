package claudecode

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// caselogSkill is embedded so `thlibo install` can mirror it into
// ~/.claude/skills/caselog/ without the user having to copy it
// manually. When the repo's SKILL.md changes, a reinstall picks up
// the new version with the same SHA-stamp / conflict semantics as
// hook scripts.

//go:embed caselog-SKILL.md
var caselogSkill []byte

// CaselogSkill returns the embedded /caselog skill's SKILL.md.
// Exposed for tests and for callers that want to inspect without
// writing to disk.
func CaselogSkill() []byte { return caselogSkill }

// InstallCaselogSkill mirrors the embedded /caselog SKILL.md into
// ~/.claude/skills/caselog/SKILL.md. Uses the same SHA-stamp
// conflict semantics as hook scripts, so a user who edited their
// copy gets their edits preserved and the new version alongside as
// SKILL.md.new.
//
// skillsDir is the parent of the per-skill directory (typically
// ~/.claude/skills); the skill's own folder name is always
// "caselog".
func InstallCaselogSkill(skillsDir string) (WriteResult, error) {
	if skillsDir == "" {
		return WriteResultCreated, fmt.Errorf("claudecode: skills dir is required")
	}
	target := filepath.Join(skillsDir, "caselog", "SKILL.md")
	// Skill files are plain data, no execute bit needed. Route
	// through writeHookBytes for the SHA-stamp logic, then chmod
	// back to 0o600 (writeHookBytes sets 0o700 which is wrong for
	// a non-executable markdown file).
	result, err := writeHookBytes(target, caselogSkill, "<!-- thlibo-installed-sha: ")
	if err != nil {
		return result, err
	}
	if result == WriteResultCreated || result == WriteResultUpdated {
		// writeHookBytes stamped the file with a hash comment and
		// chmod'd 0o700. SKILL.md is markdown, not a script;
		// 0o600 is the right mode. Best-effort chmod; a failure
		// here just leaves the execute bit set, which is harmless.
		_ = os.Chmod(target, 0o600)
	}
	return result, nil
}
