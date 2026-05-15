#!/usr/bin/env bash
# thlibo-hook-version: 1
# thlibo Claude Code PreToolUse hook for the Write + Edit tools.
#
# When the user has opted in (auto_shorthand_on_write: true in
# ~/.thlibo/config.yaml) and Claude is about to write to a path
# matching the configured globs (SKILL.md / CLAUDE.md / agents.md
# / prompts/*.yaml by default), pipe the envelope through
# `thlibo shorthand-hook`. That subcommand decides whether to
# rewrite, runs the eval gate, and emits the hookSpecificOutput
# JSON Claude Code expects.
#
# This shell wrapper is intentionally thin — all JSON parsing /
# file I/O / config loading lives in the Go subcommand so it can
# be unit-tested. Our job here is just: pipe stdin to the binary,
# write its stdout back to Claude Code, exit 0 on any error.

if ! command -v thlibo >/dev/null 2>&1; then
  exit 0
fi

case "${THLIBO_DISABLED:-0}" in
  1|true|on|yes) exit 0 ;;
esac

INPUT=$(cat)
echo "$INPUT" | thlibo shorthand-hook 2>/dev/null
exit 0
