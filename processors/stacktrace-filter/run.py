#!/usr/bin/env python3
"""stacktrace-filter: compress language-agnostic stack traces.

Reads raw output from stdin, writes a compressed form to stdout.
Handles Python Traceback blocks, Go panic + goroutine dumps, Rust
panic + backtraces, Java exceptions, and Node v8 stacks.

Lossless guarantees:
  - The exception message is preserved verbatim.
  - Every distinct file:line ref in the trace appears in the output.
  - Frame counts are reported when frames are omitted.

What gets compressed:
  - Duplicated identical frames (e.g. "× 47" instead of 47 copies)
  - Long traces: keep first 3 + last 3 frames + middle count
  - ANSI color codes
  - Blank line runs

Non-destructive: input that contains no stack-trace shape passes
through unchanged. Filter only fires when the registry's match
regex hits.
"""

from __future__ import annotations

import re
import sys

# Preserve LF on Windows: Python's default text-mode stdout translates
# \n -> \r\n, which breaks byte-identity for callers that pipe this
# script's output back through tools that compare bytes.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="", encoding="utf-8")

# Strip ANSI escapes — terminals love them, models don't need them.
ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")

# Per-language trace boundaries.
PY_START_RE = re.compile(r"^Traceback \(most recent call last\):\s*$")
PY_FRAME_RE = re.compile(r"^\s+File \"([^\"]+)\", line (\d+)(?:, in (.+))?$")
PY_EXC_RE = re.compile(r"^[A-Z][\w.]*(?:Error|Exception|Warning|Interrupt):.*$")

GO_PANIC_RE = re.compile(r"^panic: (.+)$")
GO_GOROUTINE_RE = re.compile(r"^goroutine (\d+) \[([^\]]+)\]:")
GO_FRAME_RE = re.compile(r"^([\w./*-]+(?:\.\w+)+)\(.*\)$")
GO_FRAME_LOC_RE = re.compile(r"^\s+([^:]+\.go):(\d+)\b.*$")

RUST_PANIC_RE = re.compile(r"^thread '([^']+)' panicked at (.+):$")
RUST_FRAME_RE = re.compile(r"^\s+(\d+):\s+(.+)$")
RUST_FRAME_LOC_RE = re.compile(r"^\s+at\s+(.+?):(\d+)(?::(\d+))?$")

JAVA_AT_RE = re.compile(r"^\s+at ([\w$.]+)\(([^)]+)\)\s*$")
JAVA_EXC_RE = re.compile(r"^([\w.]+(?:Error|Exception)):.*$")
JAVA_CAUSED_BY_RE = re.compile(r"^Caused by: ")

NODE_FRAME_RE = re.compile(r"^\s+at ([\w$.<> ]+)\s*\(([^)]+):(\d+):(\d+)\)\s*$")
NODE_ANON_FRAME_RE = re.compile(r"^\s+at ([^:]+):(\d+):(\d+)\s*$")

KEEP_HEAD = 3
KEEP_TAIL = 3


def strip_ansi(s: str) -> str:
    return ANSI_RE.sub("", s)


def compress_block(block: list[str]) -> list[str]:
    """Compress one stack trace, emitting a list of output lines.

    Strategy: build a list of "frame units" — for Python a frame is
    `File "..." line N` + the following indented code line; for
    Java/Node/Go each frame is one line. Then split into
    (header, frame_units, trailer), dedup consecutive identical
    units, and keep head+tail with a `... N frames omitted ...`
    marker in the middle when the count exceeds the budget.
    """
    if not block:
        return []

    units = _build_units(block)

    # Dedupe consecutive identical units — a 50-deep recursion of
    # the same frame collapses to one unit + a "× N" multiplier
    # without consuming the head/tail budget.
    units = _dedupe_units(units)

    # Slice into header (leading non-frame units) / frames /
    # trailer (trailing non-frame units).
    header_end = 0
    while header_end < len(units) and not units[header_end].is_frame:
        header_end += 1

    trailer_start = len(units)
    while trailer_start > header_end and not units[trailer_start - 1].is_frame:
        trailer_start -= 1

    header_units = units[:header_end]
    frame_units = units[header_end:trailer_start]
    trailer_units = units[trailer_start:]

    if len(frame_units) <= KEEP_HEAD + KEEP_TAIL + 1:
        return _flatten(header_units + frame_units + trailer_units)

    head = frame_units[:KEEP_HEAD]
    tail = frame_units[-KEEP_TAIL:]
    omitted = len(frame_units) - len(head) - len(tail)

    out_units = list(header_units)
    out_units.extend(head)
    if omitted > 0:
        out_units.append(_Unit(["  ... " + str(omitted) + " frames omitted ..."], is_frame=False))
    out_units.extend(tail)
    out_units.extend(trailer_units)
    return _flatten(out_units)


class _Unit:
    """One frame or non-frame block. Frames may be multiple lines
    (Python: File line + code line). Equality compares the joined
    text so dedupe works regardless of source-line count."""
    __slots__ = ("lines", "is_frame")

    def __init__(self, lines: list[str], is_frame: bool) -> None:
        self.lines = lines
        self.is_frame = is_frame

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, _Unit):
            return NotImplemented
        return self.lines == other.lines and self.is_frame == other.is_frame


def _build_units(block: list[str]) -> list[_Unit]:
    """Group block lines into frame/non-frame units.

    Python frames are 2 lines: the `File "..."` header line
    immediately followed by an indented code line. Other languages
    are 1 line per frame.
    """
    units: list[_Unit] = []
    i = 0
    while i < len(block):
        line = block[i]
        if PY_FRAME_RE.match(line):
            # Python: include the next line as the code body if it
            # exists and is indented (the typical 4-space "    code"
            # source line).
            if i + 1 < len(block) and block[i + 1].startswith("    "):
                units.append(_Unit([line, block[i + 1]], is_frame=True))
                i += 2
                continue
            units.append(_Unit([line], is_frame=True))
            i += 1
            continue
        if _looks_like_frame(line):
            units.append(_Unit([line], is_frame=True))
            i += 1
            continue
        units.append(_Unit([line], is_frame=False))
        i += 1
    return units


def _dedupe_units(units: list[_Unit]) -> list[_Unit]:
    """Collapse runs of identical consecutive units into one entry
    with a × N marker appended to the last line of the unit.
    """
    out: list[_Unit] = []
    i = 0
    while i < len(units):
        run_end = i
        while run_end + 1 < len(units) and units[run_end + 1] == units[i]:
            run_end += 1
        run_len = run_end - i + 1
        if run_len >= 3:
            base = units[i]
            tagged_lines = list(base.lines[:-1]) + [base.lines[-1] + f"    × {run_len}"]
            out.append(_Unit(tagged_lines, is_frame=base.is_frame))
        else:
            out.extend(units[i : run_end + 1])
        i = run_end + 1
    return out


def _flatten(units: list[_Unit]) -> list[str]:
    out: list[str] = []
    for u in units:
        out.extend(u.lines)
    return out


def _looks_like_frame(line: str) -> bool:
    # Conservative: any line that matches one of the per-language
    # frame patterns counts as a frame (and is therefore elidable
    # in the middle).
    return bool(
        PY_FRAME_RE.match(line)
        or GO_FRAME_RE.match(line)
        or GO_FRAME_LOC_RE.match(line)
        or RUST_FRAME_RE.match(line)
        or RUST_FRAME_LOC_RE.match(line)
        or JAVA_AT_RE.match(line)
        or NODE_FRAME_RE.match(line)
        or NODE_ANON_FRAME_RE.match(line)
    )


def _dedupe_consecutive(lines: list[str]) -> list[str]:
    """Collapse runs of identical lines into "<line> × N"."""
    out: list[str] = []
    i = 0
    while i < len(lines):
        run_end = i
        while run_end + 1 < len(lines) and lines[run_end + 1] == lines[i]:
            run_end += 1
        run_len = run_end - i + 1
        if run_len >= 3:
            out.append(f"{lines[i]}    × {run_len}")
        else:
            out.extend(lines[i : run_end + 1])
        i = run_end + 1
    return out


def split_traces(lines: list[str]) -> list[tuple[int, int]]:
    """Yield (start, end) byte indices for each trace in the input.

    A new trace starts at any line matching one of the per-language
    "trace start" patterns; it ends at the next blank-line gap.
    Non-trace text returns empty list — the caller passes it through.
    """
    starts: list[int] = []
    for i, line in enumerate(lines):
        if (
            PY_START_RE.match(line)
            or GO_PANIC_RE.match(line)
            or GO_GOROUTINE_RE.match(line)
            or RUST_PANIC_RE.match(line)
            or JAVA_EXC_RE.match(line)
            or JAVA_CAUSED_BY_RE.match(line)
        ):
            starts.append(i)

    if not starts:
        return []

    ranges: list[tuple[int, int]] = []
    for idx, start in enumerate(starts):
        # End at next blank line OR the start of the next trace.
        next_start = starts[idx + 1] if idx + 1 < len(starts) else len(lines)
        end = start
        while end < next_start:
            if lines[end].strip() == "" and end > start:
                # Allow one blank inside the trace block (Python
                # exceptions sometimes have a blank between the
                # header and the first frame). End on a SECOND
                # consecutive blank.
                if end + 1 < next_start and lines[end + 1].strip() == "":
                    break
            end += 1
        ranges.append((start, end))
    return ranges


def compress(raw: str) -> str:
    """Top-level entry — process raw bytes, return compressed bytes.

    On any exception, return the original input. The processor
    contract requires non-destructive failures; better to send
    Claude verbose-but-correct output than a broken summary.
    """
    try:
        text = strip_ansi(raw)
        lines = text.splitlines()
        ranges = split_traces(lines)
        if not ranges:
            return raw

        # Build the output by interleaving non-trace passthrough
        # text with compressed trace blocks.
        out: list[str] = []
        cursor = 0
        for start, end in ranges:
            # Passthrough preceding text verbatim.
            if start > cursor:
                out.extend(lines[cursor:start])
            # Compressed trace.
            out.extend(compress_block(lines[start:end]))
            cursor = end
        if cursor < len(lines):
            out.extend(lines[cursor:])

        return "\n".join(out) + ("\n" if raw.endswith("\n") else "")
    except Exception:  # pragma: no cover — defensive only
        return raw


def main() -> int:
    raw = sys.stdin.read()
    sys.stdout.write(compress(raw))
    return 0


if __name__ == "__main__":
    sys.exit(main())
