#!/usr/bin/env bash
# thlibo-hook-version: 1
# thlibo GitHub Copilot CLI postToolUse hook.
#
# Copilot's postToolUse hook can REPLACE a tool's result via
# `modifiedResult` (docs.github.com/copilot/reference/hooks-reference):
#
#   { "modifiedResult": { "resultType":"success",
#                         "textResultForLlm":"<compressed>" } }
#
# This is thlibo's compression path — the same observable effect as
# Codex's PostToolUse decision:block. The flow:
#
#   1. Copilot ran a tool, captured its result.
#   2. postToolUse fires; stdin carries toolResult.textResultForLlm.
#   3. We pipe that through `thlibo compress` → compressed bytes.
#   4. We emit modifiedResult with the compressed text.
#   5. Copilot substitutes it for the original in the model's context.
#
# postToolUse is FAIL-OPEN (a non-zero exit / crash / timeout is logged
# and the run continues with the ORIGINAL result), so a bug here can't
# break the client — but we still exit 0 on every passthrough so no
# noise is logged.
#
# Double-compression guard: if the preToolUse hook already wrapped the
# command as `<thlibo> exec -- …`, the output was ALREADY compressed by
# the middleware inside `thlibo exec`. Re-running `thlibo compress` on it
# would reroute already-filtered output through the LLM compressor and
# could mangle it. So when toolArgs shows a thlibo-wrapped command, we
# pass through untouched.
#
# Requires: jq, thlibo on PATH. Missing either → passthrough (exit 0).

if ! command -v jq >/dev/null 2>&1; then
  echo "[thlibo] WARNING: jq not installed; Copilot postToolUse hook disabled." >&2
  exit 0
fi
if ! command -v thlibo >/dev/null 2>&1; then
  echo "[thlibo] WARNING: thlibo not on PATH; Copilot postToolUse hook disabled." >&2
  exit 0
fi

# Per-shell kill switch; see THREAT_MODEL.md finding #16.
case "${THLIBO_DISABLED:-0}" in
  1|true|on|yes) exit 0 ;;
esac

INPUT=$(cat)

# Double-compression guard: skip results whose command was wrapped by
# our own preToolUse hook (`… exec -- …` / `thlibo exec …`).
CMD=$(jq -r '.toolArgs.command // .toolArgs.cmd // .toolArgs.script // empty' <<<"$INPUT" 2>/dev/null)
case "$CMD" in
  *"exec -- "*|*"thlibo exec"*) exit 0 ;;
esac

# The model-visible tool output. Fall back to a couple of alternate
# shapes; empty → nothing to do.
OUTPUT=$(jq -r '
  .toolResult.textResultForLlm //
  .toolResult.output           //
  .toolResult                  |
  if type == "string" then . else empty end
' <<<"$INPUT" 2>/dev/null)

if [ -z "$OUTPUT" ]; then
  exit 0
fi

# Mirror the middleware's 2000-BYTE short-circuit at the hook level to
# avoid spending a subprocess on already-small output. Measure BYTES,
# not characters: under a UTF-8 locale ${#OUTPUT} counts characters, so
# multibyte output (CJK, emoji) would be under-counted and wrongly
# skipped. `wc -c` counts bytes regardless of locale. (The middleware
# re-gates by bytes authoritatively, so this is only a fork-avoidance
# optimization — but keep it accurate so large multibyte output still
# gets compressed.)
OUT_LEN=$(printf '%s' "$OUTPUT" | wc -c)
if [ "$OUT_LEN" -lt 2000 ]; then
  exit 0
fi

COMPRESSED=$(printf '%s' "$OUTPUT" | thlibo compress 2>/dev/null)
COMPRESSED_LEN=$(printf '%s' "$COMPRESSED" | wc -c)

# Only substitute if compression actually shrank the output; otherwise
# leave the original result alone (fail open). Both lengths are byte
# counts (wc -c) so the comparison is apples-to-apples.
if [ -z "$COMPRESSED" ] || [ "$COMPRESSED_LEN" -ge "$OUT_LEN" ]; then
  exit 0
fi

jq -cn --arg text "$COMPRESSED" '
  {
    "modifiedResult": {
      "resultType": "success",
      "textResultForLlm": $text
    },
    "additionalContext": "Tool output compressed by thlibo before the model read it."
  }' 2>/dev/null || exit 0
