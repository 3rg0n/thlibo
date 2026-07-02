#!/usr/bin/env bash
# thlibo-hook-version: 1
# thlibo Cursor IDE preToolUse hook for the Shell tool.
#
# Cursor cannot substitute a shell command's OUTPUT — afterShellExecution
# is observe-only and updated_mcp_tool_output is MCP-only
# (cursor.com/docs/hooks). But preToolUse CAN rewrite the Shell tool's
# INPUT via updated_input. So thlibo takes the same command-wrap path it
# uses for Claude Code's Bash tool: rewrite `git status` to
# `thlibo exec -- git status`, which runs the command and pipes its
# output through the middleware before the model reads it.
#
# Reads the preToolUse envelope from stdin, extracts tool_input.command
# (only for tool_name == "Shell"), asks `thlibo rewrite` whether to wrap
# it, and emits Cursor's {permission, updated_input} JSON if rewriting.
#
# Exit-code protocol from `thlibo rewrite`:
#   0 + stdout   rewrite applied, stdout = new command -> emit updated_input + allow
#   1            no wrapper for argv[0]                -> exit 0 (passthrough)
#   2            deny rule                             -> exit 0 (let Cursor decide)
#   other        internal error / reserved            -> exit 0 (passthrough, never break client)
#
# Note: Cursor's preToolUse permission enum is only "allow"/"deny" —
# "ask" is accepted by the schema but NOT enforced for preToolUse
# (cursor.com/docs/hooks), so there is no ask path here. (`thlibo
# rewrite` reserves exit 3 for a future ask rule; on Cursor it would
# just fall through to passthrough.)
#
# Requires: jq, thlibo binary on PATH.

if ! command -v jq >/dev/null 2>&1; then
  echo "[thlibo] WARNING: jq not installed; Cursor hook disabled." >&2
  exit 0
fi

if ! command -v thlibo >/dev/null 2>&1; then
  echo "[thlibo] WARNING: thlibo not on PATH; Cursor hook disabled." >&2
  exit 0
fi

# Per-shell kill switch; see THREAT_MODEL.md finding #16.
case "${THLIBO_DISABLED:-0}" in
  1|true|on|yes) exit 0 ;;
esac

INPUT=$(cat)

# Tolerate invalid JSON escapes from Cursor (shell-escaped paths/args
# like `\(` or `\ ` are not legal JSON escapes and make jq bail — #62).
# If the raw input doesn't parse, strip backslashes preceding a
# NON-escape char (valid escapes \" \\ \/ \b \f \n \r \t \u kept) and
# retry. Valid input skips this untouched.
if ! printf '%s' "$INPUT" | jq -e . >/dev/null 2>&1; then
  INPUT=$(printf '%s' "$INPUT" | sed 's#\\\([^"\\/bfnrtu]\)#\1#g')
fi

# Only act on the built-in Shell tool. Cursor fires preToolUse for every
# tool; a non-Shell tool (or a missing command) is passed through.
TOOL=$(jq -r '.tool_name // empty' <<<"$INPUT")
if [ "$TOOL" != "Shell" ]; then
  exit 0
fi

CMD=$(jq -r '.tool_input.command // empty' <<<"$INPUT")
if [ -z "$CMD" ]; then
  exit 0
fi

REWRITTEN=$(thlibo rewrite "$CMD" 2>/dev/null)
EXIT_CODE=$?

case $EXIT_CODE in
  0)
    if [ -z "$REWRITTEN" ] || [ "$REWRITTEN" = "$CMD" ]; then
      exit 0
    fi
    ;;
  *)
    # Passthrough (1 = no wrapper, 2 = deny, 3 = reserved ask,
    # internal error, or anything else). Never break the client.
    exit 0
    ;;
esac

# Strip any trailing newline jq wouldn't want in the final JSON.
REWRITTEN=${REWRITTEN%$'\n'}

# Rewrite + auto-allow the wrapped command. updated_input carries the
# changed field only; Cursor merges it over the original tool_input.
jq -cn --arg cmd "$REWRITTEN" \
  '{ "permission": "allow", "updated_input": { "command": $cmd } }'
