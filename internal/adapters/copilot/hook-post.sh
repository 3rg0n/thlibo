#!/usr/bin/env bash
# thlibo-hook-version: 2
# thlibo postToolUse hook — GitHub Copilot CLI (output replacement) and
# VS Code Copilot / Claude Code (observe-only; no-op here).
#
# Only the Copilot CLI's postToolUse can REPLACE a tool's result, via
# `modifiedResult` (docs.github.com/copilot/reference/hooks-reference).
# VS Code's and Claude Code's PostToolUse are side-effect-only — they
# expose no field to substitute the model-visible output — so on those
# envelopes shell output is instead compressed by the preToolUse
# command-wrap (see hook-pre.sh), and THIS hook simply passes through.
#
# Envelope detection:
#   Copilot CLI    : { "toolResult": { "textResultForLlm" } }  -> replace
#   VS Code/Claude : { "tool_response" / "tool_input", no toolResult } -> no-op
#
# On the CLI envelope the flow is:
#   1. Copilot ran a tool, captured its result.
#   2. postToolUse fires; stdin carries toolResult.textResultForLlm.
#   3. We pipe it through `thlibo compress` -> compressed bytes.
#   4. We emit modifiedResult with the compressed text.
#
# postToolUse is FAIL-OPEN on all hosts (a non-zero exit / crash /
# timeout is logged and the original result survives), but we still
# exit 0 on every passthrough to keep the log quiet.
#
# Double-compression guard: if preToolUse already wrapped the command as
# `<thlibo> exec -- ...`, the output was ALREADY compressed inside
# `thlibo exec`; re-compressing could mangle it, so we pass through.
#
# Requires: jq, thlibo on PATH. Missing either -> passthrough (exit 0).

if ! command -v jq >/dev/null 2>&1; then
  echo "[thlibo] WARNING: jq not installed; postToolUse hook disabled." >&2
  exit 0
fi
if ! command -v thlibo >/dev/null 2>&1; then
  echo "[thlibo] WARNING: thlibo not on PATH; postToolUse hook disabled." >&2
  exit 0
fi

# Per-shell kill switch; see THREAT_MODEL.md finding #16.
case "${THLIBO_DISABLED:-0}" in
  1|true|on|yes) exit 0 ;;
esac

INPUT=$(cat)

# Only the Copilot CLI envelope (toolResult) supports output
# replacement. VS Code / Claude Code postToolUse is observe-only — the
# preToolUse command-wrap already handled compression there — so pass
# through untouched.
HAS_TOOLRESULT=$(jq -r 'has("toolResult")' <<<"$INPUT" 2>/dev/null)
if [ "$HAS_TOOLRESULT" != "true" ]; then
  exit 0
fi

# Double-compression guard: skip results whose command was wrapped by
# our own preToolUse hook (`... exec -- ...` / `thlibo exec ...`).
CMD=$(jq -r '.toolArgs.command // .toolArgs.commandLine // .toolArgs.cmd // .toolArgs.script // empty' <<<"$INPUT" 2>/dev/null)
case "$CMD" in
  *"exec -- "*|*"thlibo exec"*) exit 0 ;;
esac

# The model-visible tool output. Fall back to a couple of alternate
# shapes; empty -> nothing to do.
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
