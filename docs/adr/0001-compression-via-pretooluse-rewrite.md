# 0001. Compression via PreToolUse rewrite, not proxy or PATH shim

- Status: accepted
- Date: 2026-05-12

## Context

Thlibo needs to sit between an AI coding agent and the commands it
runs so that large tool outputs (git diffs, npm installs, test
runs, log files) can be compressed before they reach the model's
context. Three candidate mechanisms were evaluated:

1. **PostToolUse hook** — the intuitive choice given the name,
   but confirmed against Claude Code's official docs: PostToolUse
   *cannot* rewrite `tool_output`. Hooks fire after the output is
   already attached to the conversation; they can only observe,
   block the turn, or inject `additionalContext`. This shape is
   also not changing — *"PostToolUse hooks cannot undo actions
   since the tool has already executed."*

2. **PATH shim** (shadowing `git`, `npm`, etc. with thlibo wrappers
   on PATH). Small, no hook plumbing. But AI clients don't reliably
   inherit shell-session PATH into their Bash subprocess, and
   built-in tools like `Read`, `Grep`, `Glob` bypass shell entirely.

3. **HTTPS proxy on `ANTHROPIC_BASE_URL`** — universal coverage
   (intercepts every tool_result on the way back to the model), but
   signs us up for long-term compatibility with every new Anthropic
   content block type. Significant ongoing maintenance cost.

4. **PreToolUse + `updatedInput` rewrite** — RTK's mechanism.
   PreToolUse fires before execution and accepts a JSON response
   that *rewrites* the tool's arguments. We point the rewritten
   command at `thlibo exec -- <original>`. The subprocess runs the
   real command, pipes stdout through our middleware, and emits
   compressed bytes. Claude Code captures that as the tool_output.

## Decision

Use PreToolUse + `updatedInput` as the compression entry point on
Claude Code. Use PostToolUse + `decision: block` on Codex (which,
uniquely among the AI clients surveyed, lets PostToolUse replace
the tool result with a reason string).

The HTTPS proxy is deferred to v0.2+ as an optional mechanism for
users who need coverage of non-Bash tools.

## Consequences

**Easier:**

- No Anthropic API version coupling — we never touch the wire.
- Works uniformly for any Bash-invoked command, including
  user-added binaries, without modifying PATH.
- Same mechanism is one of very few affordances both Claude Code
  and Codex expose, even though they exchange roles (PreToolUse
  vs PostToolUse).

**Harder:**

- Bash-only coverage. `Read`/`Grep`/`Glob` and MCP tools bypass
  the Bash rewrite path. Acceptable for v0.1 because all of the
  spec's token-savings-table examples are Bash-produced output.
  Documented as a known limitation.
- Claude Code's model can sometimes pick a tool other than Bash
  for output a human would expect to come from Bash — we get
  less coverage than the hook docs' matcher implies.

## References

- [Claude Code hooks reference](https://code.claude.com/docs/en/hooks)
- [Codex hooks reference](https://developers.openai.com/codex/hooks)
- [RTK's hook script](https://github.com/rtk-ai/rtk/blob/develop/hooks/claude/rtk-rewrite.sh) (inspiration)
- Spec section: `.plan/thlibo-spec.md` §Client adapters
