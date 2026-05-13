#!/usr/bin/env bash
# thlibo Codex PostToolUse hook.
#
# Unlike Claude Code (where PostToolUse can only observe), Codex's
# PostToolUse supports `decision: "block"` with a `reason` field that
# *replaces* the tool result the model sees. The docs say:
#
#   "Codex records the feedback, replaces the tool result with that
#    feedback, and continues the model from the hook-provided message."
#
# That's our compression hook. The flow:
#
#   1. Codex ran `git status`, captured its stdout.
#   2. PostToolUse fires, tool_response carries the output.
#   3. We extract tool_response.output, pipe it through
#      `thlibo compress`, which returns the compressed bytes.
#   4. We emit {"decision": "block", "reason": "<compressed>"}.
#   5. Codex substitutes the compressed string for the original
#      tool result in the model's context.
#
# The hook is scoped to Bash tool calls via matcher "^Bash$". For
# tool_response shapes we don't understand, we exit 0 so the
# original output flows through unchanged.
#
# Requires: jq, thlibo on PATH.
# Requires Codex feature flag: `[features] codex_hooks = true` in ~/.codex/config.toml.

if ! command -v jq >/dev/null 2>&1; then
  echo "[thlibo] WARNING: jq not installed; Codex hook disabled." >&2
  exit 0
fi
if ! command -v thlibo >/dev/null 2>&1; then
  echo "[thlibo] WARNING: thlibo not on PATH; Codex hook disabled." >&2
  exit 0
fi

INPUT=$(cat)

# Codex's Bash tool_response shape isn't explicitly documented in
# the public docs (the public docs point at the generated schema
# files for the exact wire format). We look for common keys and
# fall through to the full tool_response if none match. The
# middleware handles whatever we feed it; worst case we compress
# a JSON envelope, which still saves tokens on verbose responses.
OUTPUT=$(jq -r '
  .tool_response.output          //
  .tool_response.stdout          //
  .tool_response.text            //
  (.tool_response | tostring)    //
  empty
' <<<"$INPUT")

if [ -z "$OUTPUT" ]; then
  exit 0
fi

# Short-circuit at the hook level too: if the output is already
# small, no point spending a subprocess on it. Middleware has its
# own 2000-byte threshold; mirroring it here avoids the fork.
# (Bytes, not chars — close enough for ASCII tool output.)
OUT_LEN=${#OUTPUT}
if [ "$OUT_LEN" -lt 2000 ]; then
  exit 0
fi

COMPRESSED=$(printf '%s' "$OUTPUT" | thlibo compress 2>/dev/null)
COMPRESSED_LEN=${#COMPRESSED}

# If compression didn't actually shrink things (registry had no
# matching processor, pipeline fell through to passthrough), leave
# the original tool result alone rather than needlessly rerouting
# it through the decision-block path.
if [ "$COMPRESSED_LEN" -ge "$OUT_LEN" ] || [ -z "$COMPRESSED" ]; then
  exit 0
fi

jq -c --arg reason "$COMPRESSED" \
  '{
    "decision": "block",
    "reason": $reason,
    "hookSpecificOutput": {
      "hookEventName": "PostToolUse",
      "additionalContext": "Tool output compressed by thlibo before the model read it."
    }
  }' <<<"{}"
