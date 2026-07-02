#!/usr/bin/env bash
# thlibo-hook-version: 1
# thlibo Cursor IDE preToolUse hook for the Read tool.
#
# When Cursor reads a log-shaped or PDF file above a size threshold,
# build a thlibo case directory (compressed.log + summary.md +
# meta.json) and rewrite tool_input.file_path to the compressed variant
# via Cursor's updated_input, so the model sees the small version.
#
# Unlike the Claude Code Read hook (which substitutes file *contents*
# after the read), Cursor's preToolUse rewrites the *path* BEFORE the
# read — so `thlibo case` must finish before we respond. A `timeout`
# guard bounds that: a slow scanned-PDF OCR (~5-30s) that would block
# Cursor instead falls through to passthrough, and Cursor reads the
# original. Tune with THLIBO_READ_TIMEOUT (seconds; default 20).
#
# Exit behaviour:
#   - match + case built    -> emit {permission:allow, updated_input:{file_path}}
#   - no match / small /     -> exit 0 silent, Cursor reads the original.
#     low-value / timeout /     (thlibo case returns ExitLowValue=6 for
#     error                      scanned PDFs / binaries / tiny files, so
#                                CASE_EXIT != 0 already covers low-value.)
#
# Requires: jq, thlibo binary on PATH. `timeout` (coreutils) optional —
# if absent, thlibo case runs unbounded (still bounded by thlibo's own
# inferd request timeout).

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

# Only act on the built-in Read tool.
TOOL=$(jq -r '.tool_name // empty' <<<"$INPUT")
if [ "$TOOL" != "Read" ]; then
  exit 0
fi

PATH_IN=$(jq -r '.tool_input.file_path // empty' <<<"$INPUT")
if [ -z "$PATH_IN" ]; then
  exit 0
fi
if [ ! -f "$PATH_IN" ]; then
  exit 0
fi

# Extension gate. Log-shaped extensions + PDF (which the model can't
# usefully read as bytes). Other files (source, configs, images) pass
# through unmodified.
EXT_LOWER=$(printf '%s' "${PATH_IN##*.}" | tr '[:upper:]' '[:lower:]')
case "$EXT_LOWER" in
  log|ndjson|txt|out|err|stderr|trace|dump|pdf) ;;
  *) exit 0 ;;
esac

# Size gate. Under the threshold, reading the file directly is cheap.
# PDFs skip the gate: not usefully readable as bytes at any size.
MIN_BYTES=${THLIBO_READ_MIN_BYTES:-32768}
if [ "$EXT_LOWER" != "pdf" ]; then
  SIZE=$(wc -c < "$PATH_IN" 2>/dev/null | tr -d ' ')
  if [ -z "$SIZE" ] || [ "$SIZE" -lt "$MIN_BYTES" ]; then
    exit 0
  fi
fi

# Don't double-compress a file that already lives inside a case dir.
case "$PATH_IN" in
  *"/.thlibo/cases/"*|*"\\.thlibo\\cases\\"*) exit 0 ;;
esac

# Build the case, bounded by a timeout so a slow OCR can't hang Cursor.
# If `timeout` isn't installed, run unbounded (thlibo has its own
# inferd request timeout as a backstop).
TIMEOUT_SECS=${THLIBO_READ_TIMEOUT:-20}
if command -v timeout >/dev/null 2>&1; then
  CASE_DIR=$(timeout "${TIMEOUT_SECS}s" thlibo case --quiet "$PATH_IN" 2>/dev/null)
else
  CASE_DIR=$(thlibo case --quiet "$PATH_IN" 2>/dev/null)
fi
CASE_EXIT=$?

# CASE_EXIT != 0 covers: timeout (124), low-value (ExitLowValue=6),
# and any error. In all of those Cursor reads the original.
if [ "$CASE_EXIT" -ne 0 ] || [ -z "$CASE_DIR" ]; then
  exit 0
fi

COMPRESSED="${CASE_DIR}/compressed.log"
if [ ! -f "$COMPRESSED" ]; then
  exit 0
fi

# Normalise a Windows path so Cursor/JSON handle it cleanly.
COMPRESSED=$(printf '%s' "$COMPRESSED" | sed 's#\\#/#g')

jq -cn --arg newpath "$COMPRESSED" \
  '{ "permission": "allow", "updated_input": { "file_path": $newpath } }'
