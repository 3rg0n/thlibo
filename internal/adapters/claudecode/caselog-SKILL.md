---
name: caselog
description: When the user drops a log file, error dump, or large text output into the conversation (or references one by path), use this skill to run `thlibo case <path>` first. thlibo compresses the file into a small summary plus structured metadata under `~/.thlibo/cases/`; you then read THAT summary instead of the raw file, saving tokens. Use this for files over ~32 KB that look like logs, traces, stack dumps, CI output, or similar repetitive content.
---

# caselog — compress-before-read workflow for large log files

## What this skill does

When you're about to read a big, noisy file (typically a log, stack
trace, CI output, or similar), don't `Read` it directly. Instead:

1. Run `thlibo case <path>` as a Bash tool call. It prints the case
   directory to stdout, something like:

       /Users/alice/.thlibo/cases/20260514-153000-3a2b19f0

2. Read `<case-dir>/summary.md` for the human header.

3. Read `<case-dir>/compressed.log` for the actual compressed
   content. This is the file the user cares about — everything else
   is bookkeeping.

4. If the user asks follow-up questions that the compressed form
   can't answer, `Read` the original path directly at that point. The
   compressed form is a starting point, not a replacement.

## When to invoke this

Use when any of these is true:

- The user dragged or attached a file with a log-shaped extension
  (`.log`, `.ndjson`, `.txt`, `.out`, `.err`, `.trace`, `.dump`).
- The user pasted a path to a large file and asked you to look at
  it.
- You already tried `Read` on a file and got truncation or more
  than ~1000 lines back — `thlibo case` will produce a narrower
  view of the same content.

Do **not** use for:

- Source code files — compression is tuned for log-like repetition,
  not code.
- Small config files (under ~32 KB). Just `Read` them.
- Binary files. `thlibo case` would silently copy them verbatim and
  waste a directory.

## Example invocation

User says: "look at `/tmp/build.log`, something's wrong in the
deploy step"

Your flow:

```
Bash: thlibo case /tmp/build.log
# → /Users/alice/.thlibo/cases/20260514-153000-3a2b19f0
Read: /Users/alice/.thlibo/cases/20260514-153000-3a2b19f0/summary.md
Read: /Users/alice/.thlibo/cases/20260514-153000-3a2b19f0/compressed.log
```

Then analyse the compressed form and answer the user.

## Configuration

- `~/.thlibo/cases/` is the case root. Override via
  `$THLIBO_CASES_DIR`.
- Disable globally for a session with `THLIBO_DISABLED=1`.
- Prune old cases: `thlibo case --prune 168h` removes everything
  older than a week.

## If `thlibo` isn't installed

`thlibo case` will not be on `$PATH`. Tell the user to run the
installer from https://github.com/3rg0n/thlibo, then fall back to
reading the file directly.
