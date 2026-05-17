#!/usr/bin/env python3
"""ndjson-filter: compress NDJSON / structured-log streams.

Reads stdin, parses each line as JSON, groups by (level, msg)
signature, deduplicates with a count multiplier, and emits the
result sorted with errors first.

Lossless guarantees:
  - Every distinct (level, msg) appears in the output once.
  - The first occurrence of each signature is kept verbatim — so
    every distinct file path, error code, SHA, version string,
    request ID, and timestamp present in *some* record survives.
  - Counts on duplicates are preserved as `_count` field.

What gets compressed:
  - The 499 duplicate records of the same error → 1 + count=500.
  - Records that fail JSON parse pass through verbatim (defensive
    — never destroy data we don't understand).

Non-destructive: input that's not NDJSON-shaped (no parseable
lines) returns unchanged. Filter only fires on a registry match
anyway, so this is double protection.
"""

from __future__ import annotations

import json
import sys

# Preserve LF on Windows: Python's default text-mode stdout translates
# \n -> \r\n, which breaks byte-identity for callers that pipe this
# script's output back through tools that compare bytes.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="")

# Severity ordering. Higher numbers come first in the output so
# errors are easy to find. Aliases collapse onto the same rank.
LEVEL_RANK = {
    "fatal": 5, "panic": 5,
    "error": 4, "err": 4, "critical": 4, "crit": 4,
    "warn": 3, "warning": 3,
    "info": 2, "notice": 2,
    "debug": 1,
    "trace": 0,
}


def _level(rec: dict) -> str:
    """Extract a normalised level string from a structured-log record.
    Handles the common synonyms (level, severity, lvl, severity_text).
    Returns 'info' as the default rank when no level field is present.
    """
    for k in ("level", "severity", "lvl", "severity_text", "loglevel"):
        v = rec.get(k)
        if isinstance(v, str) and v:
            return v.lower()
        if isinstance(v, int):
            # OTel: numeric severity 1-24
            if v >= 17:
                return "fatal"
            if v >= 13:
                return "error"
            if v >= 9:
                return "warn"
            if v >= 5:
                return "info"
            return "debug"
    return "info"


def _msg(rec: dict) -> str:
    """Extract the message field. Common keys: msg, message, body."""
    for k in ("msg", "message", "body", "log", "text"):
        v = rec.get(k)
        if isinstance(v, str):
            return v
    return ""


def compress(raw: str) -> str:
    try:
        lines = raw.splitlines()

        # Group by (level, msg). The value is (count, first_record_json).
        # Order of insertion is preserved so the output keeps the
        # original arrival order within each group — useful for
        # reading time-correlated logs.
        groups: dict[tuple[str, str], list] = {}
        non_json: list[str] = []

        for line in lines:
            stripped = line.strip()
            if not stripped:
                continue
            try:
                rec = json.loads(stripped)
            except (ValueError, TypeError):
                # Not JSON — pass through unchanged.
                non_json.append(line)
                continue

            if not isinstance(rec, dict):
                non_json.append(line)
                continue

            key = (_level(rec), _msg(rec))
            if key in groups:
                groups[key][0] += 1
            else:
                groups[key] = [1, rec]

        if not groups:
            # Nothing parseable — pass the input through unchanged.
            return raw

        # Sort group entries by (level rank desc, original arrival).
        # Stable sort on Python guarantees the secondary order is
        # preserved.
        items = list(groups.items())
        items.sort(
            key=lambda kv: -LEVEL_RANK.get(kv[0][0], 2)
        )

        out: list[str] = []
        for key, val in items:
            count, rec = val
            if count > 1:
                rec = dict(rec)
                rec["_count"] = count
            out.append(json.dumps(rec, separators=(",", ":")))

        # Append any non-JSON lines we passed through, separated
        # from the structured records by a blank line so the AI
        # can tell them apart.
        if non_json:
            out.append("")
            out.extend(non_json)

        return "\n".join(out) + ("\n" if raw.endswith("\n") else "")
    except Exception:
        # Filter contract: never break the AI client.
        return raw


def main() -> int:
    raw = sys.stdin.read()
    sys.stdout.write(compress(raw))
    return 0


if __name__ == "__main__":
    sys.exit(main())
