#!/usr/bin/env bash
# thlibo-hook-version: 1
# thlibo Claude Code PreToolUse hook for the Read tool.
#
# When Claude reads a log-shaped file above a size threshold, build a
# thlibo case directory (compressed.log + summary.md + meta.json) and
# rewrite tool_input.file_path to the compressed variant so Claude
# sees the small version.
#
# Exit behaviour:
#   - match + success  -> emit hookSpecificOutput with updatedInput.file_path
#   - no match / small / error -> exit 0 silent, Claude reads the original
#
# Requires: jq, thlibo binary on PATH.

if ! command -v jq >/dev/null 2>&1; then
  exit 0
fi
if ! command -v thlibo >/dev/null 2>&1; then
  exit 0
fi

case "${THLIBO_DISABLED:-0}" in
  1|true|on|yes) exit 0 ;;
esac

INPUT=$(cat)
PATH_IN=$(jq -r '.tool_input.file_path // empty' <<<"$INPUT")
if [ -z "$PATH_IN" ]; then
  exit 0
fi

if [ ! -f "$PATH_IN" ]; then
  exit 0
fi

# Extension gate. Log-shaped extensions + binary formats Claude
# can't read natively. Other files (source code, configs, images)
# pass through to Claude unmodified.
EXT_LOWER=$(printf '%s' "${PATH_IN##*.}" | tr '[:upper:]' '[:lower:]')
case "$EXT_LOWER" in
  log|ndjson|txt|out|err|stderr|trace|dump|pdf) ;;
  *) exit 0 ;;
esac

# Size gate. Under the threshold, Claude reading the file directly
# is cheap — don't spend a daemon call on a 100-line config.
# PDFs skip the size gate: Claude can't usefully read PDF bytes at
# any size, so conversion is always worth running.
MIN_BYTES=${THLIBO_READ_MIN_BYTES:-32768}
if [ "$EXT_LOWER" != "pdf" ]; then
  SIZE=$(wc -c < "$PATH_IN" 2>/dev/null | tr -d ' ')
  if [ -z "$SIZE" ] || [ "$SIZE" -lt "$MIN_BYTES" ]; then
    exit 0
  fi
fi

# Don't double-compress a file that already lives inside a case dir.
# "${HOME}/.thlibo/cases/" might not be expanded if $HOME is unset in
# Claude Code's hook env; do the best we can.
case "$PATH_IN" in
  *"/.thlibo/cases/"*|*"\\.thlibo\\cases\\"*) exit 0 ;;
esac

CASE_DIR=$(thlibo case --quiet "$PATH_IN" 2>/dev/null)
CASE_EXIT=$?
if [ "$CASE_EXIT" -ne 0 ] || [ -z "$CASE_DIR" ]; then
  # Silent failure: Claude reads the original. Never break the client.
  exit 0
fi

COMPRESSED="${CASE_DIR}/compressed.log"
if [ ! -f "$COMPRESSED" ]; then
  exit 0
fi

jq -c --arg newpath "$COMPRESSED" --arg src "$PATH_IN" '
  .tool_input.file_path = $newpath |
  {
    "hookSpecificOutput": {
      "hookEventName": "PreToolUse",
      "permissionDecision": "allow",
      "permissionDecisionReason": ("thlibo auto-compressed: " + $src),
      "updatedInput": .tool_input
    }
  }
' <<<"$INPUT"
