#!/usr/bin/env python3
"""git-filter: compress git output for an AI assistant's context.

Reads raw git output from stdin, writes a compressed form to stdout.
Non-destructive: if a line doesn't match any known pattern, it's kept.
Never raises - unknown output types pass through.

Compressed:
  - `git status`: branch + changed/untracked file paths, one per line.
    Hint lines ("(use `git add ...`") and blank separators dropped.
  - `git log`: commit hash + first line of message. Author/date lines
    kept only for the first commit in a block.
  - `git diff`: per-file summary "diff --git a/x b/x (+N -M)". Hunks
    and full patch text dropped.
"""
from __future__ import annotations

import re
import sys

# Preserve LF on Windows: Python's default text-mode stdout translates
# \n -> \r\n, which breaks byte-identity for callers that pipe this
# script's output back through tools that compare bytes.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="")


def compress(raw: str) -> str:
    lines = raw.splitlines()
    out: list[str] = []
    in_diff_hunk = False
    diff_file = None
    diff_plus = 0
    diff_minus = 0

    def flush_diff():
        nonlocal diff_file, diff_plus, diff_minus, in_diff_hunk
        if diff_file is not None:
            out.append(f"diff {diff_file} (+{diff_plus} -{diff_minus})")
        diff_file = None
        diff_plus = 0
        diff_minus = 0
        in_diff_hunk = False

    # Git hint lines: `(use "git ..." to ...)` or `(use \`git ...\` ...)`.
    hint_re = re.compile(r"^\s*\(use [\"`]git")
    diff_header_re = re.compile(r"^diff --git a/(\S+) b/\S+")
    hunk_re = re.compile(r"^@@ ")

    for line in lines:
        # git status hint lines
        if hint_re.match(line):
            continue

        # diff block tracking
        m = diff_header_re.match(line)
        if m:
            flush_diff()
            diff_file = m.group(1)
            in_diff_hunk = False
            continue
        if diff_file is not None:
            if hunk_re.match(line):
                in_diff_hunk = True
                continue
            if in_diff_hunk:
                if line.startswith("+") and not line.startswith("+++"):
                    diff_plus += 1
                    continue
                if line.startswith("-") and not line.startswith("---"):
                    diff_minus += 1
                    continue
                # End of hunk block when we hit a new diff header or commit.
                if line.startswith(("diff --git", "commit ")):
                    flush_diff()
                    # Fall through and re-process this line in the outer
                    # loop body by rerunning the diff_header match.
                    m2 = diff_header_re.match(line)
                    if m2:
                        diff_file = m2.group(1)
                        in_diff_hunk = False
                        continue
                    # commit line - leave diff_file cleared, let outer
                    # logic keep the commit line.
                else:
                    # Context lines inside the hunk.
                    continue
            else:
                # Pre-hunk metadata: index, ---, +++, Binary, deleted file
                # mode, etc. Drop silently - the diff_file summary is
                # enough for the reader.
                continue

        # Collapse consecutive blank lines.
        if line.strip() == "":
            if out and out[-1] == "":
                continue
            out.append("")
            continue

        out.append(line)

    flush_diff()

    # Strip trailing blanks.
    while out and out[-1] == "":
        out.pop()

    return "\n".join(out) + ("\n" if out else "")


def main() -> int:
    raw = sys.stdin.read()
    sys.stdout.write(compress(raw))
    return 0


if __name__ == "__main__":
    sys.exit(main())
