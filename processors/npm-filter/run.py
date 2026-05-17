#!/usr/bin/env python3
"""npm-filter: compress npm output for an AI assistant's context.

Reads raw npm output from stdin, writes a compressed form to stdout.
Never raises - unknown output passes through.

Compressed:
  - `npm list`: drops dependency tree glyphs, keeps one `name@version`
    line per package, deduplicated.
  - `npm install`: keeps "added N packages" / "removed N packages"
    lines; drops per-package progress; keeps all npm ERR! / npm WARN.
  - `npm audit`: keeps vulnerability heading lines and severity
    counts; drops fix-suggestion prose.
"""
from __future__ import annotations

import re
import sys

# Preserve LF on Windows: Python's default text-mode stdout translates
# \n -> \r\n, which breaks byte-identity for callers that pipe this
# script's output back through tools that compare bytes.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="")


TREE_GLYPH_RE = re.compile(r"^\s*[+\-`]+\s*(?:UNMET DEPENDENCY\s+)?(?P<name>.+?)\s*$")
NPM_HEADER_RE = re.compile(r"^npm (error|ERR!|warn|WARN|notice)")
AUDIT_SEV_RE = re.compile(r"^\s*(low|moderate|high|critical)\s+severity", re.IGNORECASE)
COUNTS_RE = re.compile(r"^(added|removed|changed|audited|up to date)\s+\d")
PKG_LINE_RE = re.compile(r"^[A-Za-z0-9_@\-/.]+@[\d.\w\-+]+$")


def compress(raw: str) -> str:
    lines = raw.splitlines()
    out: list[str] = []
    seen_notice: set[str] = set()
    for line in lines:
        stripped = line.strip()
        if not stripped:
            if out and out[-1] != "":
                out.append("")
            continue

        # Always keep ERR!/WARN/error/notice lines, but dedupe notices.
        m = NPM_HEADER_RE.match(stripped)
        if m:
            if m.group(1) in ("notice", "NOTICE"):
                if stripped in seen_notice:
                    continue
                seen_notice.add(stripped)
            out.append(stripped)
            continue

        # "added N packages" / "audited 123 packages in 4s"
        if COUNTS_RE.match(stripped):
            out.append(stripped)
            continue

        # Audit severity lines
        if AUDIT_SEV_RE.match(stripped):
            out.append(stripped)
            continue

        # Plain package@version (from `npm list` after glyph strip)
        if PKG_LINE_RE.match(stripped):
            out.append(stripped)
            continue

        # Tree-glyph lines - pull out the bare package@version.
        tm = TREE_GLYPH_RE.match(line)
        if tm:
            name = tm.group("name").strip()
            if PKG_LINE_RE.match(name):
                out.append(name)
                continue
            # Drop otherwise (deduped fluff).
            continue

        # "# npm audit report" style headings survive.
        if stripped.startswith("#"):
            out.append(stripped)
            continue

        # Progress bars, "npm fund", fix prose - drop.
        continue

    # Collapse trailing blanks.
    while out and out[-1] == "":
        out.pop()
    return "\n".join(out) + ("\n" if out else "")


def main() -> int:
    sys.stdout.write(compress(sys.stdin.read()))
    return 0


if __name__ == "__main__":
    sys.exit(main())
