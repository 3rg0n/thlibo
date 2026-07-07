#!/usr/bin/env bash
# thlibo-hook-version: 2
# thlibo preToolUse hook for the shell tool — GitHub Copilot CLI AND
# VS Code Copilot (1.111+, currently Insiders).
#
# Both hosts run command hooks the same way (a program that reads a JSON
# envelope on stdin and writes a JSON decision on stdout), and VS Code
# reads hook files from ~/.copilot/hooks/ — the very dir this script is
# installed into — so one file serves both. But the two hosts use
# DIFFERENT wire formats, so this hook DETECTS the envelope and replies
# in kind:
#
#   Copilot CLI   : { "toolName", "toolArgs": {command} }
#                   -> { "permissionDecision":"allow", "modifiedArgs" }
#   VS Code/Claude: { "tool_name", "tool_input": {command|commandLine} }
#                   -> { "hookSpecificOutput":
#                          { "hookEventName":"PreToolUse",
#                            "permissionDecision":"allow",
#                            "updatedInput": <tool_input w/ command swapped> } }
#
# In both hosts thlibo takes the command-wrap path: `thlibo rewrite`
# turns `git status` into `<thlibo> exec -- git status`, which runs the
# command and routes its output through the middleware. (VS Code's
# PostToolUse can't replace output — same limitation as Claude Code — so
# the pre-wrap is how shell output gets compressed there.)
#
# CRITICAL — Copilot CLI preToolUse is FAIL-CLOSED (a non-zero exit
# DENIES the tool call) and VS Code treats exit 2 as a blocking error.
# So this hook exits 0 on EVERY path and only ever emits "allow" (or
# nothing). It must never deny. `trap 'exit 0' ERR` backstops anything
# missed.
#
# Requires: jq, thlibo on PATH. Missing either -> passthrough (exit 0).

trap 'exit 0' ERR

if ! command -v jq >/dev/null 2>&1; then
  echo "[thlibo] WARNING: jq not installed; preToolUse hook disabled." >&2
  exit 0
fi
if ! command -v thlibo >/dev/null 2>&1; then
  echo "[thlibo] WARNING: thlibo not on PATH; preToolUse hook disabled." >&2
  exit 0
fi

# Per-shell kill switch; see THREAT_MODEL.md finding #16.
case "${THLIBO_DISABLED:-0}" in
  1|true|on|yes) exit 0 ;;
esac

INPUT=$(cat)

# Detect the envelope. The CLI carries "toolArgs"; VS Code/Claude carry
# "tool_input". We only need to know which container holds the command.
HAS_TOOLARGS=$(jq -r 'has("toolArgs")' <<<"$INPUT" 2>/dev/null)

# Extract the command from whichever field carries it. Different tools
# name it differently (docs type these as unknown/varied): .command is
# most common, with .cmd / .commandLine / .script as fallbacks.
if [ "$HAS_TOOLARGS" = "true" ]; then
  CONTAINER=".toolArgs"
else
  CONTAINER=".tool_input"
fi
CMD=$(jq -r "${CONTAINER}.command // ${CONTAINER}.commandLine // ${CONTAINER}.cmd // ${CONTAINER}.script // empty" <<<"$INPUT" 2>/dev/null)
if [ -z "$CMD" ]; then
  exit 0
fi

REWRITTEN=$(thlibo rewrite "$CMD" 2>/dev/null)
EXIT_CODE=$?

# Only exit 0 from `thlibo rewrite` means "wrap it". Everything else
# (1 no-wrapper, 2 deny, 3 ask, >=64 internal) is a passthrough — and on
# a fail-closed host we NEVER convert a rewrite-deny into a host deny; we
# just leave the command alone.
if [ "$EXIT_CODE" -ne 0 ] || [ -z "$REWRITTEN" ] || [ "$REWRITTEN" = "$CMD" ]; then
  exit 0
fi
REWRITTEN=${REWRITTEN%$'\n'}

if [ "$HAS_TOOLARGS" = "true" ]; then
  # --- Copilot CLI: modifiedArgs replaces toolArgs wholesale. Echo the
  # original args back with only the command field(s) swapped. ---
  jq -cn --argjson args "$(jq -c '.toolArgs' <<<"$INPUT")" --arg cmd "$REWRITTEN" '
    ($args // {}) as $a
    | {
        "permissionDecision": "allow",
        "permissionDecisionReason": "thlibo auto-rewrite (output compression)",
        "modifiedArgs": (
          $a
          | (if has("command")     then .command     = $cmd else . end)
          | (if has("commandLine") then .commandLine = $cmd else . end)
          | (if has("cmd")         then .cmd         = $cmd else . end)
          | (if has("script")      then .script      = $cmd else . end)
        )
      }' 2>/dev/null || exit 0
else
  # --- VS Code / Claude Code: hookSpecificOutput.updatedInput carries a
  # full copy of tool_input with the command field swapped. ---
  jq -cn --argjson ti "$(jq -c '.tool_input // {}' <<<"$INPUT")" --arg cmd "$REWRITTEN" '
    ($ti // {}) as $t
    | {
        "hookSpecificOutput": {
          "hookEventName": "PreToolUse",
          "permissionDecision": "allow",
          "permissionDecisionReason": "thlibo auto-rewrite (output compression)",
          "updatedInput": (
            $t
            | (if has("command")     then .command     = $cmd else . end)
            | (if has("commandLine") then .commandLine = $cmd else . end)
            | (if has("cmd")         then .cmd         = $cmd else . end)
            | (if has("script")      then .script      = $cmd else . end)
          )
        }
      }' 2>/dev/null || exit 0
fi
