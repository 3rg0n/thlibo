#!/usr/bin/env python3
"""ndjson-filter: compress NDJSON / structured-log streams.

Reads stdin, parses each line as JSON, groups by a
(level, msg, method, status-class, path-shape) signature,
deduplicates with a count multiplier, and emits the result sorted
with errors first.

Guarantees:
  - Every distinct signature appears in the output once. The
    signature includes HTTP access-log fields (method, status class,
    normalised path) so access logs keep their route/status
    distribution instead of collapsing to one row (#27). Records with
    no HTTP fields contribute empty components, so the signature
    reduces to (level, msg) — generic logs behave exactly as before.
  - The first occurrence of each signature is kept verbatim.
  - Counts on duplicates are preserved as `_count` field.
  - Note: distinctness is captured by the signature fields, not every
    field — two records that differ only in an arbitrary field (e.g.
    `user`) still collapse. The signature targets the high-value
    access-log case, not universal per-field preservation.

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


# HTTP access-log field synonyms. Traefik/nginx/envoy/k8s ingress and app
# request logs emit a constant level+msg for every request, so a
# (level,msg) signature collapses the whole stream to one row and drops
# every path/status/method distinction (#27). These pull the fields that
# actually vary into the signature. All return "" when absent — a generic
# log record then contributes empty components and the signature reduces
# to (level,msg), preserving prior behaviour exactly.
def _http_field(rec: dict, *keys) -> str:
    for k in keys:
        v = rec.get(k)
        if isinstance(v, str) and v:
            return v
        if isinstance(v, (int, float)) and not isinstance(v, bool):
            return str(int(v))
    return ""


def _ascii_digits(s: str) -> bool:
    """True iff s is non-empty and all ASCII 0-9. NOT str.isdigit(),
    which also accepts Unicode numerals ('²', '٣') — the Go port uses an
    ASCII 0-9 loop, so this keeps the two byte-identical (ADR 0010)."""
    return bool(s) and all("0" <= c <= "9" for c in s)


def _method(rec: dict) -> str:
    return _http_field(rec, "method", "RequestMethod", "http_method", "verb", "requestMethod")


def _status_class(rec: dict) -> str:
    """Status class ('5xx') not the exact code, so 500 vs 503 on the same
    route still collapse — the class is what a 'which paths 5xx'd'
    question needs, and it keeps compression high."""
    s = _http_field(rec, "status", "DownstreamStatus", "http_status",
                    "statusCode", "status_code", "response_code", "OriginStatus")
    if len(s) == 3 and _ascii_digits(s) and s[0] in "12345":
        return s[0] + "xx"
    return s


def _seg_variable(s: str) -> bool:
    """A high-cardinality path segment (numeric id, uuid, long hex token)
    rather than a stable route word."""
    if _ascii_digits(s):
        return True
    if len(s) == 36 and s[8] == "-" and s[13] == "-" and s[18] == "-" and s[23] == "-":
        return True
    if len(s) >= 24 and all(c in "0123456789abcdefABCDEF" for c in s):
        return True
    return False


def _path_shape(rec: dict) -> str:
    """Normalise a URL path to a template: /api/users/1007 -> /api/users/<var>
    so per-id requests collapse while distinct routes stay separate."""
    p = _http_field(rec, "RequestPath", "path", "url", "uri", "target",
                    "http_path", "request_uri", "requestPath")
    if not p:
        return ""
    for sep in ("?", "#"):
        i = p.find(sep)
        if i >= 0:
            p = p[:i]
    segs = p.split("/")
    return "/".join("<var>" if s and _seg_variable(s) else s for s in segs)


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

            key = (_level(rec), _msg(rec), _method(rec),
                   _status_class(rec), _path_shape(rec))
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
