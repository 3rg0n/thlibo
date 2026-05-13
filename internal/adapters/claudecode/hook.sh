#!/usr/bin/env bash
# thlibo-hook-version: 1
# thlibo Claude Code PreToolUse hook for the Bash tool.
#
# Reads the tool envelope from stdin, extracts tool_input.command,
# asks `thlibo rewrite` whether to wrap it, and emits the Claude
# Code hookSpecificOutput JSON if rewriting.
#
# Exit-code protocol from thlibo rewrite:
#   0 + stdout   rewrite applied, stdout = new command -> emit updatedInput
#   1            no wrapper for argv[0]                -> exit 0 (passthrough)
#   2            deny rule (v0.2)                      -> exit 0 (let Claude Code handle)
#   3 + stdout   ask rule (v0.2)                       -> emit updatedInput w/o permissionDecision
#   other        internal error                        -> exit 0 (passthrough, never break client)
#
# Requires: jq, thlibo binary on PATH.

if ! command -v jq >/dev/null 2>&1; then
  echo "[thlibo] WARNING: jq not installed; hook disabled." >&2
  exit 0
fi

if ! command -v thlibo >/dev/null 2>&1; then
  echo "[thlibo] WARNING: thlibo not on PATH; hook disabled." >&2
  exit 0
fi

INPUT=$(cat)
CMD=$(jq -r '.tool_input.command // empty' <<<"$INPUT")

if [ -z "$CMD" ]; then
  # Not a Bash tool call or no command - let Claude Code proceed.
  exit 0
fi

REWRITTEN=$(thlibo rewrite "$CMD" 2>/dev/null)
EXIT_CODE=$?

case $EXIT_CODE in
  0)
    # Rewrite: emit updatedInput + auto-allow.
    if [ -z "$REWRITTEN" ] || [ "$REWRITTEN" = "$CMD" ]; then
      exit 0
    fi
    ;;
  3)
    # Ask: rewrite but let Claude Code prompt the user.
    if [ -z "$REWRITTEN" ]; then
      exit 0
    fi
    ;;
  *)
    # Passthrough (1, 2, internal error, or anything else).
    exit 0
    ;;
esac

# Strip any trailing newline jq wouldn't want in the final JSON.
REWRITTEN=${REWRITTEN%$'\n'}

if [ "$EXIT_CODE" -eq 3 ]; then
  # Ask: omit permissionDecision so Claude Code prompts.
  jq -c --arg cmd "$REWRITTEN" \
    '.tool_input.command = $cmd | {
      "hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "updatedInput": .tool_input
      }
    }' <<<"$INPUT"
else
  # Allow: rewrite + auto-allow.
  jq -c --arg cmd "$REWRITTEN" \
    '.tool_input.command = $cmd | {
      "hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "allow",
        "permissionDecisionReason": "thlibo auto-rewrite",
        "updatedInput": .tool_input
      }
    }' <<<"$INPUT"
fi
