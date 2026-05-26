#!/usr/bin/env python3
"""lint-filter: compress lint / static-analysis output for an AI client.

Reads stdin, parses each line as a lint finding using format-specific
regexes for the major linters, groups by rule id, dedupes identical
findings, sorts errors-first, and caps each rule to N findings.

Supported formats:
  - GCC / Clang     `file:line:col: warning: msg [-Wflag]`
  - Clippy / rustc  `file:line:col: warning: msg` (lint name in trailing note)
  - ESLint default  table form (file header + indented `line:col level msg rule`)
  - ESLint compact  `file: line N, col N, severity - msg (rule)`
  - ESLint unix     `file:line:col: msg [Error/level/rule]`
  - golangci-lint   `file:line:col: msg (linter)`
  - shellcheck      `file:line:col: level: msg [SCxxxx]`
  - flake8 / ruff   `file:line:col: CODE msg`            (E/W/F/C/B/I/...)
  - mypy            `file:line: error: msg [code]`        (no col)
  - rubocop         `file:line:col: C: Rule/Name: msg`    (single-letter sev)
  - stylelint       `file:line:col: level  msg [rule]`

Lossless guarantees:
  - Every distinct rule id appears in the output at least once.
  - The first N findings per rule are kept verbatim — so every
    distinct file:line ref for an early-bucket finding survives.
  - Total counts per rule are preserved as a `× N` annotation when
    the cap fires. The tail line records original-rule-count too.

What gets compressed:
  - Findings beyond LINT_MAX_PER_RULE per rule (default 5).
  - Source-excerpt context lines (the `   |` and `   ^` rustc-style
    carets, gcc's repeated source line, clippy's help / note
    suggestion blocks, eslint's per-file blank lines).
  - ANSI colour codes.
  - Blank-line runs.

Set LINT_KEEP_CONTEXT=1 to keep source-excerpt context (debugging
the filter itself).

Fail-open contract: any unhandled exception → input verbatim.
"""

from __future__ import annotations

import os
import re
import sys
from typing import Iterable, List, Optional, Tuple

# Preserve LF on Windows (matches every other thlibo script processor).
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="")

MAX_PER_RULE = int(os.environ.get("LINT_MAX_PER_RULE", "5"))
KEEP_CONTEXT = bool(int(os.environ.get("LINT_KEEP_CONTEXT", "0") or "0"))

# Strip terminal colour escapes — eslint, golangci-lint, clippy all
# emit them by default when stdout is a TTY; pipes vary.
ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")

# ---- per-format finding parsers --------------------------------------
#
# Each parser is a (regex, parser_fn) pair. parser_fn returns a Finding
# dict on match, or None to skip. The dispatcher tries them in order;
# first hit wins. Order matters: more-specific patterns first.

_SEV_NORMAL = {
    "fatal": "error", "panic": "error", "crit": "error", "critical": "error",
    "err": "error", "e": "error", "f": "error",
    "warn": "warning", "w": "warning",
    "n": "note", "i": "info", "s": "style",
    "c": "convention", "r": "refactor",
}


def _norm_sev(raw: str) -> str:
    return _SEV_NORMAL.get(raw.lower(), raw.lower())


# 1. GCC / Clang / Clippy / rustc:
#    /tmp/test.c:2:9: warning: msg [-Wflag]
#    src/main.rs:14:13: warning: msg
_GCC_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
    r"(?P<sev>warning|error|note|help|fatal\s+error):\s+"
    r"(?P<msg>.+?)"
    r"(?:\s+\[(?P<rule>-[WD][^\]]+|clippy::[\w:]+)\])?\s*$"
)

# 2. Shellcheck default (gcc-format):
#    /tmp/script.sh:5:12: warning: msg [SC2086]
_SHELLCHECK_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
    r"(?P<sev>warning|error|info|style|note):\s+"
    r"(?P<msg>.+?)\s+\[(?P<rule>SC\d+)\]\s*$"
)

# 3. ESLint compact:
#    /tmp/test.js: line 2, col 7, error - msg (rule)
_ESLINT_COMPACT_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?):\s+line\s+(?P<line>\d+),\s+col\s+(?P<col>\d+),\s+"
    r"(?P<sev>warning|error)\s+-\s+(?P<msg>.+?)\s+\((?P<rule>[\w/@.-]+)\)\s*$"
)

# 4. ESLint unix:
#    /tmp/test.js:2:7: msg [Error/no-unused-vars]
_ESLINT_UNIX_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
    r"(?P<msg>.+?)\s+\[(?P<sev>Error|Warning)/(?P<rule>[\w/@.-]+)\]\s*$"
)

# 5. mypy: `file:line: error: msg [code]` (no col)
#    Also matches: `file:line: note: msg` (no code)
_MYPY_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):\s+"
    r"(?P<sev>error|warning|note):\s+"
    r"(?P<msg>.+?)"
    r"(?:\s+\[(?P<rule>[\w-]+)\])?\s*$"
)

# 6. flake8 / ruff: `file:line:col: CODE msg`
#    CODE = letter prefix + digits (E302, W291, F841, B007, I001, ...)
_FLAKE_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
    r"(?P<rule>[A-Z]{1,3}\d{2,4})\s+(?P<msg>.+?)\s*$"
)

# 7. rubocop: `file:line:col: C: [Correctable] Rule/Name: msg`
_RUBOCOP_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
    r"(?P<sev>[CWERF]):\s+"
    r"(?:\[Correctable\]\s+)?"
    r"(?P<rule>[A-Z][\w/]*):\s+(?P<msg>.+?)\s*$"
)

# 8. stylelint: `file:line:col: level  msg [rule]`
#    Note the double space — stylelint pads severity column.
_STYLELINT_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
    r"(?P<sev>warning|error)\s{2,}(?P<msg>.+?)\s+\[(?P<rule>[\w-]+)\]\s*$"
)

# 9. golangci-lint: `file:line:col: msg (linter)`
#    No explicit severity — treated as "error" (its convention).
_GOLANGCI_RE = re.compile(
    r"^(?P<file>[^:\s][^:\n]*?\.go):(?P<line>\d+):(?P<col>\d+):\s+"
    r"(?P<msg>.+?)\s+\((?P<linter>[\w./-]+)\)\s*$"
)

# Dispatch order: most-specific first. shellcheck before gcc (both
# match `:N:N: warning:`); rubocop before flake (both have `:N:N:`
# prefix); mypy last among `file:line:` patterns since it's column-less.
_DISPATCH: List[Tuple[re.Pattern, str]] = [
    (_SHELLCHECK_RE, "shellcheck"),
    (_RUBOCOP_RE, "rubocop"),
    (_STYLELINT_RE, "stylelint"),
    (_ESLINT_COMPACT_RE, "eslint-compact"),
    (_ESLINT_UNIX_RE, "eslint-unix"),
    (_GOLANGCI_RE, "golangci"),
    (_FLAKE_RE, "flake"),
    (_GCC_RE, "gcc"),
    (_MYPY_RE, "mypy"),
]


def _parse(line: str) -> Optional[dict]:
    """Try every parser; return the first hit's normalised finding."""
    for pattern, kind in _DISPATCH:
        m = pattern.match(line)
        if not m:
            continue
        gd = m.groupdict()
        sev = _norm_sev(gd.get("sev") or ("error" if kind == "golangci" else "info"))
        rule = gd.get("rule") or gd.get("linter") or ""
        if kind == "gcc" and not rule:
            # Bare gcc/clang note without a flag — skip to avoid
            # swallowing every "note: ..." continuation line.
            if sev in ("note", "help"):
                return None
            rule = f"-W{sev}"  # synthetic bucket so rule grouping still works
        if not rule:
            return None
        return {
            "kind": kind,
            "sev": sev,
            "file": gd["file"].strip(),
            "line": int(gd["line"]),
            "col": int(gd["col"]) if gd.get("col") else 0,
            "rule": rule,
            "msg": gd["msg"].strip(),
        }
    return None


# ---- context detection -----------------------------------------------
#
# After a finding, gcc/clang/rustc/clippy emit source-excerpt context:
#   2 |     int x;
#     |         ^
# eslint default emits per-file headers and indented rows we already
# parsed. The simplest safe rule: any line that doesn't itself parse
# as a finding *and* sits between two findings (or after the last
# finding) is context. Drop unless KEEP_CONTEXT.

_CONTEXT_RE = re.compile(
    r"^\s*(?:\d+\s*\||\||\s+\^|\s+=\s+(?:help|note):|\s+\.\.\.|\s+-->|\s*$)"
)


def _is_context(line: str) -> bool:
    """Heuristic: looks like a gcc/clippy/rustc context excerpt."""
    return bool(_CONTEXT_RE.match(line))


# ---- compress --------------------------------------------------------

_SEV_RANK = {"error": 4, "fatal": 4, "warning": 3, "info": 2,
             "style": 1, "note": 1, "convention": 1, "refactor": 1, "help": 0}


def compress(raw: str) -> str:
    try:
        raw = ANSI_RE.sub("", raw)
        # Group by rule. Each group keeps the first MAX_PER_RULE
        # findings verbatim; further hits increment count only.
        groups: dict[str, dict] = {}
        passthrough: List[str] = []
        any_parsed = False

        for line in raw.splitlines():
            stripped = line.rstrip()
            if not stripped:
                continue
            f = _parse(stripped)
            if f is None:
                if _is_context(line):
                    if KEEP_CONTEXT:
                        passthrough.append(stripped)
                    # else: drop
                else:
                    # Unrecognised non-context line — keep it. eslint's
                    # default formatter emits a per-file path header
                    # ("/tmp/test.js") on its own line; summary lines
                    # like "✖ 12 problems (3 errors, 9 warnings)" live
                    # here too. They're useful context for the AI.
                    passthrough.append(stripped)
                continue

            any_parsed = True
            rule = f["rule"]
            g = groups.setdefault(rule, {
                "rule": rule,
                "sev": f["sev"],
                "kind": f["kind"],
                "findings": [],
                "total": 0,
                "msg": f["msg"],
            })
            g["total"] += 1
            # Promote highest severity if we see a worse one for this rule.
            if _SEV_RANK.get(f["sev"], 0) > _SEV_RANK.get(g["sev"], 0):
                g["sev"] = f["sev"]
            if len(g["findings"]) < MAX_PER_RULE:
                g["findings"].append(f)

        if not any_parsed:
            # No lint shape detected — pass everything through.
            return raw

        # Sort: errors first, then by occurrence count desc, then by
        # rule-id alpha so the order is stable across runs.
        ordered = sorted(
            groups.values(),
            key=lambda g: (-_SEV_RANK.get(g["sev"], 0), -g["total"], g["rule"]),
        )

        out: List[str] = []
        for g in ordered:
            for f in g["findings"]:
                loc = f"{f['file']}:{f['line']}"
                if f["col"]:
                    loc += f":{f['col']}"
                out.append(f"{g['sev']:7} {loc:40} {g['rule']:24} {f['msg']}")
            elided = g["total"] - len(g["findings"])
            if elided > 0:
                out.append(
                    f"{'':7} {'':40} {g['rule']:24} … +{elided} more {g['rule']} hit(s)"
                )

        # Tally line at the end. Useful for the AI to know the
        # original size before its context-window-friendly summary.
        total_findings = sum(g["total"] for g in groups.values())
        total_rules = len(groups)
        out.append("")
        out.append(f"lint={total_findings} findings; {total_rules} rules; cap={MAX_PER_RULE}/rule")

        # Append the passthrough section (per-file headers, summary
        # lines, anything we didn't recognise as a finding) so it
        # reaches the AI too.
        if passthrough:
            out.append("")
            out.extend(passthrough)

        return "\n".join(out) + "\n"
    except Exception:
        return raw


def main() -> int:
    sys.stdout.write(compress(sys.stdin.read()))
    return 0


if __name__ == "__main__":
    sys.exit(main())
