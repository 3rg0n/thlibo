#!/usr/bin/env python3
"""pytest-filter: compress pytest stdout for an AI assistant.

Lossless for the parts an AI cares about (failures, errors, summary
counts, every file:line ref in a traceback). Drops only:
  - Progress dot/letter stream after `collected N items`
  - Environment info (rootdir, plugins, configfile)
  - Per-test capture decorations
  - DeprecationWarnings whose path doesn't appear in any failure
  - "warnings summary" section if no failures reference it
  - Repeated blank lines

Non-destructive: input that doesn't look like pytest passes through.
"""

from __future__ import annotations

import re
import sys

# Preserve LF on Windows: Python's default text-mode stdout translates
# \n -> \r\n, which breaks byte-identity for callers that pipe this
# script's output back through tools that compare bytes.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="")

ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")

SECTION_RE = re.compile(r"^=+ (.+?) =+\s*$")
COLLECTED_RE = re.compile(r"^collected (\d+) items?")
PROGRESS_LINE_RE = re.compile(r"^[\w./_-]+\.py [.FEsxXp]+\s+\[\s*\d+%\s*\]\s*$")
SHORT_SUMMARY_RE = re.compile(r"^=+ (\d+ (?:passed|failed|error|skipped|warning|deselected)[^=]*) =+\s*$")
PASSED_FAILED_LINE_RE = re.compile(r"^(\d+ (?:passed|failed|error|errors|skipped|warning)s?[^=]*) in [\d.]+s")

# Blocks we always keep verbatim.
KEEP_SECTIONS = {"FAILURES", "ERRORS", "short test summary info"}

# Blocks we sometimes drop (when no failure references them).
WARNINGS_SECTION = "warnings summary"


def strip_ansi(s: str) -> str:
    return ANSI_RE.sub("", s)


def compress(raw: str) -> str:
    try:
        text = strip_ansi(raw)
        lines = text.splitlines()

        # First pass: identify every === Section === boundary so
        # we can carve up keep-vs-drop blocks. Pytest's section
        # markers are robust because every variant uses === wrap.
        sections = _find_sections(lines)
        if not sections:
            # Doesn't look like pytest output at all — pass through.
            return raw

        out: list[str] = []
        in_progress = False  # collected -> first FAILURES/short summary
        last_blank = False

        i = 0
        while i < len(lines):
            line = lines[i]

            # Section boundary?
            if i in sections:
                section_name = sections[i]
                end = sections.get_end(i)

                if section_name in KEEP_SECTIONS:
                    out.extend(lines[i:end])
                    in_progress = False
                    last_blank = False
                    i = end
                    continue

                if section_name == WARNINGS_SECTION:
                    # Keep only if any FAILURES exist; pytest already
                    # signals that via the short summary's failed count.
                    if _has_failures(lines):
                        out.extend(lines[i:end])
                    i = end
                    last_blank = False
                    continue

                if section_name.startswith("test session starts"):
                    # Keep the section header; suppress env info
                    # block underneath until we hit `collected N`.
                    out.append(line)
                    last_blank = False
                    i += 1
                    while i < len(lines) and not COLLECTED_RE.match(lines[i]):
                        i += 1
                    continue

                # Unknown section — keep as-is. Better to be loud
                # than silently drop.
                out.extend(lines[i:end])
                i = end
                continue

            # Within the in-progress dot stream (between `collected`
            # and the next section), drop progress lines but keep
            # any line that's a real failure marker (`F` start of
            # block, etc.).
            if COLLECTED_RE.match(line):
                out.append(line)
                in_progress = True
                last_blank = False
                i += 1
                continue

            if in_progress:
                if PROGRESS_LINE_RE.match(line):
                    # Drop the per-file dot line.
                    i += 1
                    continue

            # Compact blank-line runs.
            if line.strip() == "":
                if not last_blank:
                    out.append(line)
                last_blank = True
                i += 1
                continue
            last_blank = False

            # Tail summary line ("N passed, M failed in 1.23s")
            # — always keep.
            if PASSED_FAILED_LINE_RE.match(line):
                out.append(line)
                i += 1
                continue

            out.append(line)
            i += 1

        return "\n".join(out) + ("\n" if raw.endswith("\n") else "")
    except Exception:
        # Filter contract: never break the AI client.
        return raw


class _SectionMap:
    """Maps each section's start-line index to its name, plus an
    end-line lookup so callers can extract slices in one pass.
    """

    def __init__(self, starts: list[int], names: list[str], total: int) -> None:
        self._starts = starts
        self._names = names
        self._total = total
        self._lookup = {s: n for s, n in zip(starts, names)}

    def __contains__(self, idx: int) -> bool:
        return idx in self._lookup

    def __getitem__(self, idx: int) -> str:
        return self._lookup[idx]

    def get(self, idx: int, default=None):
        return self._lookup.get(idx, default)

    def get_end(self, start_idx: int) -> int:
        """Return the line index just past the end of the section
        starting at start_idx. Section ends at the next section
        boundary or end-of-input.
        """
        for s in self._starts:
            if s > start_idx:
                return s
        return self._total


def _find_sections(lines: list[str]) -> _SectionMap:
    starts: list[int] = []
    names: list[str] = []
    for i, line in enumerate(lines):
        m = SECTION_RE.match(line)
        if m:
            starts.append(i)
            names.append(m.group(1).strip())
    return _SectionMap(starts, names, len(lines))


def _has_failures(lines: list[str]) -> bool:
    for line in lines:
        m = SECTION_RE.match(line)
        if m and m.group(1).strip() == "FAILURES":
            return True
        m2 = SHORT_SUMMARY_RE.match(line)
        if m2 and "failed" in m2.group(1):
            return True
    return False


def main() -> int:
    raw = sys.stdin.read()
    sys.stdout.write(compress(raw))
    return 0


if __name__ == "__main__":
    sys.exit(main())
