#!/usr/bin/env bash
# thlibo-hook-version: 1
# thlibo GitHub Copilot CLI preToolUse hook for the shell tool.
#
# Copilot's preToolUse hook can rewrite a tool's INPUT before it runs,
# via `modifiedArgs` (docs.github.com/copilot/reference/hooks-reference).
# So thlibo takes the same command-wrap path it uses for Claude Code's
# Bash tool: `thlibo rewrite` turns `git status` into
# `<thlibo> exec -- git status`, which runs the command and routes its
# output through the middleware before the model reads it.
#
# CRITICAL — Copilot preToolUse is FAIL-CLOSED: a non-zero exit (or a
# crash) DENIES the tool call, which would break the client. So this
# hook exits 0 on EVERY path and only ever emits permissionDecision
# "allow" (or nothing at all). It must never deny. (A timeout is
# fail-open per the docs, so a hang is safe too, but we never block.)
#
# stdin  (Copilot preToolUse envelope, camelCase):
#   { "sessionId","timestamp","cwd","toolName","toolArgs": {...} }
# stdout (to rewrite): { "permissionDecision":"allow", "modifiedArgs": {...} }
#   modifiedArgs replaces toolArgs wholesale, so we echo the original
#   args back with only the command field swapped.
#
# The shell tool's name is "bash"/"shell"/"powershell" and the command
# lives under an argument whose exact key the docs leave as `unknown`;
# we probe the common shapes (.command // .cmd // .script). A non-shell
# tool, or an unrecognised arg shape, passes through untouched (no
# output + exit 0 = Copilot proceeds normally).
#
# Requires: jq, thlibo on PATH. Missing either → passthrough (exit 0).

# Fail-closed guard: from here on, ANY unexpected error must still exit
# 0. We keep the logic simple and defensive rather than relying on a
# trap, but a trap backstops anything we missed.
trap 'exit 0' ERR

if ! command -v jq >/dev/null 2>&1; then
  echo "[thlibo] WARNING: jq not installed; Copilot preToolUse hook disabled." >&2
  exit 0
fi
if ! command -v thlibo >/dev/null 2>&1; then
  echo "[thlibo] WARNING: thlibo not on PATH; Copilot preToolUse hook disabled." >&2
  exit 0
fi

# Per-shell kill switch; see THREAT_MODEL.md finding #16.
case "${THLIBO_DISABLED:-0}" in
  1|true|on|yes) exit 0 ;;
esac

INPUT=$(cat)

# Extract the command from whichever toolArgs field carries it. The
# docs type toolArgs as `unknown`; .command is the most likely shape,
# with .cmd / .script as fallbacks. Empty → passthrough.
CMD=$(jq -r '.toolArgs.command // .toolArgs.cmd // .toolArgs.script // empty' <<<"$INPUT" 2>/dev/null)
if [ -z "$CMD" ]; then
  exit 0
fi

REWRITTEN=$(thlibo rewrite "$CMD" 2>/dev/null)
EXIT_CODE=$?

# Only exit 0 from `thlibo rewrite` means "wrap it". Everything else
# (1 no-wrapper, 2 deny, 3 ask, >=64 internal) is a passthrough — and
# on a FAIL-CLOSED host we NEVER convert a deny into a Copilot deny;
# we just leave the command alone.
if [ "$EXIT_CODE" -ne 0 ] || [ -z "$REWRITTEN" ] || [ "$REWRITTEN" = "$CMD" ]; then
  exit 0
fi

REWRITTEN=${REWRITTEN%$'\n'}

# Echo toolArgs back with the command field replaced. We set all three
# candidate keys we might have read from so the replacement lands on
# whichever one Copilot actually uses, without inventing keys that
# weren't present.
jq -cn --argjson args "$(jq -c '.toolArgs' <<<"$INPUT")" --arg cmd "$REWRITTEN" '
  ($args // {}) as $a
  | {
      "permissionDecision": "allow",
      "permissionDecisionReason": "thlibo auto-rewrite (output compression)",
      "modifiedArgs": (
        $a
        | (if has("command") then .command = $cmd else . end)
        | (if has("cmd")     then .cmd     = $cmd else . end)
        | (if has("script")  then .script  = $cmd else . end)
      )
    }' 2>/dev/null || exit 0
