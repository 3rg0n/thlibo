# caselog skill

The canonical source for this skill lives at
[`../../internal/adapters/claudecode/caselog-SKILL.md`](../../internal/adapters/claudecode/caselog-SKILL.md).
It's embedded into the `thlibo` binary via `go:embed` and mirrored
into `~/.claude/skills/caselog/SKILL.md` by `thlibo install`.

This directory is the discovery entry-point — third parties browsing
`skills/` can see what's available without knowing the binary's
internals.

## Installation

`thlibo install` writes the skill automatically. To install manually:

```bash
mkdir -p ~/.claude/skills/caselog
cp ../../internal/adapters/claudecode/caselog-SKILL.md ~/.claude/skills/caselog/SKILL.md
```

Then invoke from Claude Code with `/caselog`.

## What the skill does

Tells Claude to run `thlibo case <path>` on a large log file before
`Read`-ing it, so the AI works from the compressed `summary.md` +
`compressed.log` instead of the raw blob. See the SKILL.md itself
for the full prompt + invocation flow.
