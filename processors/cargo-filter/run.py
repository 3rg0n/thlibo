#!/usr/bin/env python3
"""cargo-filter: compress cargo output for an AI assistant's context.

Reads raw cargo output from stdin, writes a compressed form to stdout.
Never raises - unknown output passes through.

Compressed:
  - Build: keeps `error[E...]:`, `warning:`, the `-->` file:line
    pointer that follows, and the final `Finished` line. Drops
    per-crate `Compiling` / `Downloaded` lines and the `= help:` /
    `= note:` suggestion prose.
  - Test: keeps `running N tests`, the one-line `test result:`
    summary, and every `FAILED` line. Drops per-test progress dots.
  - Clippy: same as build.
"""
from __future__ import annotations

import re
import sys

# Preserve LF on Windows: Python's default text-mode stdout translates
# \n -> \r\n, which breaks byte-identity for callers that pipe this
# script's output back through tools that compare bytes.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="")


COMPILING_RE = re.compile(r"^\s*(Compiling|Checking|Downloaded|Downloading|Updating|Generating)\s")
FINISHED_RE = re.compile(r"^\s*Finished\s")
RUNNING_RE = re.compile(r"^\s*Running\s")
ERROR_RE = re.compile(r"^error(\[E\d+\])?:")
WARNING_RE = re.compile(r"^warning:")
POINTER_RE = re.compile(r"^\s*-->")
TEST_HEADER_RE = re.compile(r"^running \d+ tests?$")
TEST_RESULT_RE = re.compile(r"^test result:")
TEST_FAILED_RE = re.compile(r"\btest .* FAILED\b|\bFAILED$")


def compress(raw: str) -> str:
    lines = raw.splitlines()
    out: list[str] = []
    keep_next_if_pointer = False

    for line in lines:
        if COMPILING_RE.match(line):
            keep_next_if_pointer = False
            continue

        if FINISHED_RE.match(line) or RUNNING_RE.match(line):
            out.append(line.rstrip())
            keep_next_if_pointer = False
            continue

        if ERROR_RE.match(line) or WARNING_RE.match(line):
            out.append(line.rstrip())
            keep_next_if_pointer = True
            continue

        if keep_next_if_pointer and POINTER_RE.match(line):
            out.append(line.rstrip())
            keep_next_if_pointer = False
            continue

        # After a pointer we drop the `= help:` / `= note:` prose
        # block until the next blank line or another error.
        keep_next_if_pointer = False

        if TEST_HEADER_RE.match(line.strip()) or TEST_RESULT_RE.match(line.strip()):
            out.append(line.rstrip())
            continue

        if TEST_FAILED_RE.search(line):
            out.append(line.rstrip())
            continue

        # Blank separators collapse to at most one.
        if line.strip() == "":
            if out and out[-1] != "":
                out.append("")
            continue

        # Nothing matched - drop.

    while out and out[-1] == "":
        out.pop()
    return "\n".join(out) + ("\n" if out else "")


def main() -> int:
    sys.stdout.write(compress(sys.stdin.read()))
    return 0


if __name__ == "__main__":
    sys.exit(main())
