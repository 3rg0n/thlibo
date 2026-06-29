#!/usr/bin/env python3
"""lint-filter: distill verbose lint output for an AI client.

Reads stdin, parses lint findings into a TSV row per finding, drops
source-snippet noise (carets, fenced source lines), preserves help
suggestions where the linter offers a fix, and returns it. If
distillation can't beat the input byte-count, returns the input
verbose. Never grows the output.

Output schema (4 columns, tab-separated):
    severity \\t file:line[:col] \\t rule-id \\t message

Optional 5th-column help row appears immediately under any finding
that had a `help: ...` suggestion in the original output, marked with
`-` in the severity column:
    - \\t same-loc \\t same-rule \\t help: msg

The AI consumes this naturally: severity-rule-loc-message in one
line, fix-suggestion (when present) on the next.

Supported input shapes:
  Verbose (multi-line; we distill):
    - clippy / rustc default      `warning: msg \\n --> file:line:col \\n  | ... \\n = help: ...`
    - ruff default                same as clippy
    - gcc / clang default         `file:line:col: warning: msg [-Wflag] \\n source-line \\n   ^`
    - eslint stylish              file header + indented `line:col level msg rule`

  Terse (single-line; we PASSTHROUGH):
    - eslint -f compact / -f unix
    - clippy --message-format=short
    - ruff --output-format=concise
    - golangci-lint, staticcheck, go vet
    - mypy, flake8, shellcheck, rubocop, stylelint
    - tsc

Verbose detection is conservative: if any line in the buffer looks
like a verbose block-opener (`warning: msg` *without* a leading
`file:line:col:` prefix, OR a `--> file:line:col` continuation),
we enter the distill path. Otherwise we passthrough verbatim.

Monotonic guarantee: distilled output is returned only if its byte-
count is smaller than the input. Otherwise the input is returned
unchanged. No input is ever made larger by this processor.
"""

from __future__ import annotations

import os
import re
import sys
from typing import List, Optional, Tuple

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="")

MAX_PER_RULE = int(os.environ.get("LINT_MAX_PER_RULE", "0") or "0")  # 0 = no cap

ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")

# ---- terse-shape parsers (one line = one finding) --------------------

_TERSE_DISPATCH: List[Tuple[re.Pattern, str]] = []


def _t(pattern: str, kind: str) -> None:
    _TERSE_DISPATCH.append((re.compile(pattern), kind))


# shellcheck: file:line:col: level: msg [SCxxxx]
_t(r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
   r"(?P<sev>warning|error|info|style|note):\s+"
   r"(?P<msg>.+?)\s+\[(?P<rule>SC\d+)\]\s*$",
   "shellcheck")

# rubocop: file:line:col: C: [Correctable] Rule/Name: msg
_t(r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
   r"(?P<sev>[CWERF]):\s+(?:\[Correctable\]\s+)?"
   r"(?P<rule>[A-Z][\w/]*):\s+(?P<msg>.+?)\s*$",
   "rubocop")

# stylelint: file:line:col: level  msg [rule]   (double space)
_t(r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
   r"(?P<sev>warning|error)\s{2,}(?P<msg>.+?)\s+\[(?P<rule>[\w-]+)\]\s*$",
   "stylelint")

# eslint compact: file: line N, col N, sev - msg (rule)
_t(r"^(?P<file>[^:\s][^:\n]*?):\s+line\s+(?P<line>\d+),\s+col\s+(?P<col>\d+),\s+"
   r"(?P<sev>warning|error|Warning|Error)\s+-\s+(?P<msg>.+?)\s+\((?P<rule>[\w/@.-]+)\)\s*$",
   "eslint-compact")

# eslint unix: file:line:col: msg [Sev/rule]
_t(r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
   r"(?P<msg>.+?)\s+\[(?P<sev>Error|Warning)/(?P<rule>[\w/@.-]+)\]\s*$",
   "eslint-unix")

# golangci-lint: file:line:col: msg (linter)
_t(r"^(?P<file>[^:\s][^:\n]*?\.go):(?P<line>\d+):(?P<col>\d+):\s+"
   r"(?P<msg>.+?)\s+\((?P<linter>[\w./-]+)\)\s*$",
   "golangci")

# gosec: [file:line] - Gxxx (CWE-nn): msg (Confidence: X, Severity: Y)
# Distinct shape — bracketed loc, no column, severity in a trailing
# parenthetical rather than the usual sev: position. The trailing
# (Confidence/Severity) tail is dropped from msg; sev comes from it.
_t(r"^\[(?P<file>[^:\s\]][^:\n\]]*?):(?P<line>\d+)\]\s+-\s+"
   r"(?P<rule>G\d+)\s+\((?P<cwe>CWE-\d+)\):\s+"
   r"(?P<msg>.+?)\s+\(Confidence:\s+\w+,\s+Severity:\s+(?P<sev>HIGH|MEDIUM|LOW)\)\s*$",
   "gosec")

# flake8 / ruff concise: file:line:col: CODE msg
_t(r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
   r"(?P<rule>[A-Z]{1,3}\d{2,4})\s+(?P<msg>.+?)\s*$",
   "flake")

# clippy --message-format=short / generic gcc-style: file:line:col: sev: msg [-Wflag]?
_t(r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+"
   r"(?P<sev>warning|error|note|help|fatal\s+error):\s+"
   r"(?P<msg>.+?)"
   r"(?:\s+\[(?P<rule>-[WD][^\]]+|clippy::[\w:]+)\])?\s*$",
   "gcc-short")

# mypy: file:line: sev: msg [code]    (no col)
_t(r"^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):\s+"
   r"(?P<sev>error|warning|note):\s+"
   r"(?P<msg>.+?)(?:\s+\[(?P<rule>[\w-]+)\])?\s*$",
   "mypy")

# tsc: file(line,col): sev TSxxxx: msg
_t(r"^(?P<file>[^()\s][^()\n]*?)\((?P<line>\d+),(?P<col>\d+)\):\s+"
   r"(?P<sev>error|warning)\s+(?P<rule>TS\d+):\s+(?P<msg>.+?)\s*$",
   "tsc")


def _terse_parse(line: str) -> Optional[dict]:
    for pattern, kind in _TERSE_DISPATCH:
        m = pattern.match(line)
        if not m:
            continue
        gd = m.groupdict()
        sev = (gd.get("sev") or ("error" if kind == "golangci" else "info")).lower()
        rule = gd.get("rule") or gd.get("linter") or ""
        if not rule and kind == "gcc-short":
            if sev in ("note", "help"):
                return None
            rule = f"-W{sev}"
        if not rule:
            return None
        return {
            "kind": kind,
            "sev": _norm_sev(sev),
            "file": gd["file"].strip(),
            "line": int(gd["line"]),
            "col": int(gd["col"]) if gd.get("col") else 0,
            "rule": rule,
            "msg": gd["msg"].strip(),
            "help": "",
        }
    return None


_SEV_NORMAL = {
    "fatal": "error", "panic": "error", "crit": "error", "critical": "error",
    "err": "error", "e": "error", "f": "error",
    "warn": "warning", "w": "warning",
    "n": "note", "i": "info", "s": "style",
    "c": "convention", "r": "refactor",
    # gosec severities (trailing "Severity: HIGH|MEDIUM|LOW")
    "high": "error", "medium": "warning", "low": "info",
}


def _norm_sev(raw: str) -> str:
    return _SEV_NORMAL.get(raw.lower(), raw.lower())


# ---- verbose-shape detection -----------------------------------------

# A verbose block opens with `severity: msg` (no file prefix) followed
# by ` --> file:line:col` on the next non-empty line. Or gcc verbose:
# `file:line:col: warning: msg [-Wflag]` followed by source-snippet.

_RUSTC_OPENER = re.compile(r"^\s*(?P<sev>warning|error|note|help)(?:\[(?P<code>[\w:]+)\])?:\s+(?P<msg>.+?)\s*$")
_RUSTC_LOC = re.compile(r"^\s*-->\s+(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+)\s*$")
_RUSTC_HELP = re.compile(r"^\s*=?\s*help:\s+(?P<msg>.+?)\s*$")
_RUSTC_NOTE = re.compile(r"^\s*=\s+note:\s+`#\[\w+\((?P<rule>[\w:]+)\)\]`")
_RUSTC_RULE_TAIL = re.compile(r"\[(?P<rule>-[WD][^\]]+|clippy::[\w:]+)\]\s*$")

# Ruff verbose opener: `RULECODE [*] message` (the `[*]` marker is
# optional and means "auto-fixable"). Followed by ` --> file:line:col`.
_RUFF_OPENER = re.compile(
    r"^\s*(?P<rule>[A-Z]{1,3}\d{2,4})(?:\s+\[\*\])?\s+(?P<msg>.+?)\s*$"
)

# gcc verbose: gcc-short matches the finding line; the source/caret
# lines that follow are dropped. We detect "verbose gcc" by the
# presence of a `   N | source` line or `      | ^^^` line near a
# gcc-short hit.
_GCC_SOURCE_RE = re.compile(r"^\s*\d+\s*\|\s")
_GCC_CARET_RE = re.compile(r"^\s*\|[\s~^]+$|^\s+\^[\s~^]*$")

# eslint stylish: file path on its own line, then indented rows.
_ESLINT_FILE_RE = re.compile(r"^(?P<file>[A-Za-z]:\\|/|\\\\|\.[\\/])\S.*$")
_ESLINT_ROW_RE = re.compile(
    r"^\s+(?P<line>\d+):(?P<col>\d+)\s+(?P<sev>error|warning)\s+(?P<msg>.+?)\s+(?P<rule>[\w/@.-]+)\s*$"
)

# A buffer is "verbose-shape" if any line matches a verbose block
# opener that the terse parsers don't handle.
_VERBOSE_HINT = re.compile(
    r"(?m)"
    r"^\s*(?:warning|error|note|help)(?:\[[\w:]+\])?:\s+\S"  # rustc/clippy opener
    r"|^\s*-->\s+\S+:\d+:\d+\s*$"                            # rustc/ruff loc continuation
    r"|^\s*\d+\s*\|\s"                                       # gcc/rustc source line
    r"|^\s+(?:\d+):(?:\d+)\s+(?:error|warning)\s"            # eslint stylish row
    r"|^\s*[A-Z]{1,3}\d{2,4}(?:\s+\[\*\])?\s+\S"             # ruff verbose opener
)


def _is_verbose(raw: str) -> bool:
    return bool(_VERBOSE_HINT.search(raw))


# ---- verbose-shape parser --------------------------------------------

def _parse_verbose(raw: str) -> List[dict]:
    """Walk the buffer line-by-line; return list of findings.

    We carry forward state across lines because each finding spans
    multiple lines in verbose output.
    """
    lines = raw.splitlines()
    findings: List[dict] = []
    i = 0
    n = len(lines)

    # eslint-stylish state: last seen file header.
    eslint_file: Optional[str] = None

    while i < n:
        line = lines[i]
        stripped = line.rstrip()

        # 1. rustc/clippy/ruff multi-line block.
        m = _RUSTC_OPENER.match(stripped)
        if m and i + 1 < n and _RUSTC_LOC.match(lines[i + 1]):
            sev = m.group("sev").lower()
            msg = m.group("msg").strip()
            code_in_brackets = m.group("code")  # rustc style: error[E0382]
            # rule may also be in trailing `[clippy::...]` on the msg
            rule_match = _RUSTC_RULE_TAIL.search(msg)
            if rule_match:
                rule = rule_match.group("rule")
                msg = msg[: rule_match.start()].rstrip()
            elif code_in_brackets:
                rule = code_in_brackets
            else:
                rule = ""

            loc = _RUSTC_LOC.match(lines[i + 1])
            file = loc.group("file")
            line_no = int(loc.group("line"))
            col = int(loc.group("col"))

            # Walk forward past source/caret/blank/help/note lines to
            # collect a help suggestion if present, and pick up the
            # rule from `= note: #[warn(rule_name)]` if we don't have
            # one yet. Stop at the next opener or non-context line.
            j = i + 2
            help_text = ""
            while j < n:
                nxt = lines[j]
                if not nxt.strip():
                    j += 1
                    continue
                hm = _RUSTC_HELP.match(nxt)
                if hm and not help_text:
                    help_text = hm.group("msg").strip()
                    j += 1
                    continue
                nm = _RUSTC_NOTE.match(nxt)
                if nm and not rule:
                    rule = nm.group("rule")
                    j += 1
                    continue
                # context line (source / caret / continuation)
                if (_GCC_SOURCE_RE.match(nxt) or _GCC_CARET_RE.match(nxt)
                        or nxt.lstrip().startswith("|")
                        or nxt.lstrip().startswith("=")
                        or nxt.lstrip().startswith("...")):
                    j += 1
                    continue
                # next finding opener — stop
                if _RUSTC_OPENER.match(nxt) and j + 1 < n and _RUSTC_LOC.match(lines[j + 1]):
                    break
                # gcc verbose finding (file:line:col: ...) — stop
                if _terse_parse(nxt.rstrip()):
                    break
                # something else — skip it; it's noise
                j += 1

            if not rule:
                rule = f"-W{sev}"

            findings.append({
                "kind": "verbose-rustc",
                "sev": _norm_sev(sev),
                "file": file,
                "line": line_no,
                "col": col,
                "rule": rule,
                "msg": msg,
                "help": help_text,
            })
            i = j
            continue

        # 1b. ruff verbose: `CODE [*] msg` + ` --> file:line:col` + source/help.
        rm = _RUFF_OPENER.match(stripped)
        if rm and i + 1 < n and _RUSTC_LOC.match(lines[i + 1]):
            rule = rm.group("rule")
            msg = rm.group("msg").strip()
            loc = _RUSTC_LOC.match(lines[i + 1])
            file = loc.group("file")
            line_no = int(loc.group("line"))
            col = int(loc.group("col"))

            j = i + 2
            help_text = ""
            while j < n:
                nxt = lines[j]
                if not nxt.strip():
                    j += 1
                    continue
                hm = _RUSTC_HELP.match(nxt)
                if hm and not help_text:
                    help_text = hm.group("msg").strip()
                    j += 1
                    continue
                if (_GCC_SOURCE_RE.match(nxt) or _GCC_CARET_RE.match(nxt)
                        or nxt.lstrip().startswith("|")
                        or nxt.lstrip().startswith("=")
                        or nxt.lstrip().startswith("...")):
                    j += 1
                    continue
                # next ruff opener
                if _RUFF_OPENER.match(nxt) and j + 1 < n and _RUSTC_LOC.match(lines[j + 1]):
                    break
                if _RUSTC_OPENER.match(nxt) and j + 1 < n and _RUSTC_LOC.match(lines[j + 1]):
                    break
                if _terse_parse(nxt.rstrip()):
                    break
                j += 1

            findings.append({
                "kind": "verbose-ruff",
                "sev": "warning",
                "file": file,
                "line": line_no,
                "col": col,
                "rule": rule,
                "msg": msg,
                "help": help_text,
            })
            i = j
            continue

        # 2. gcc verbose: terse-parsable opener followed by source lines.
        f = _terse_parse(stripped)
        if f:
            # Look ahead, swallow source-snippet/caret lines.
            j = i + 1
            help_text = ""
            while j < n:
                nxt = lines[j]
                if (_GCC_SOURCE_RE.match(nxt)
                        or _GCC_CARET_RE.match(nxt)
                        or not nxt.strip()):
                    j += 1
                    continue
                hm = _RUSTC_HELP.match(nxt)
                if hm and not help_text:
                    help_text = hm.group("msg").strip()
                    j += 1
                    continue
                break
            f["help"] = help_text
            findings.append(f)
            i = j
            continue

        # 3. eslint stylish: file header + indented rows.
        if _ESLINT_FILE_RE.match(stripped) and not stripped.endswith(":"):
            eslint_file = stripped
            i += 1
            continue
        em = _ESLINT_ROW_RE.match(stripped)
        if em and eslint_file:
            findings.append({
                "kind": "eslint-stylish",
                "sev": _norm_sev(em.group("sev")),
                "file": eslint_file,
                "line": int(em.group("line")),
                "col": int(em.group("col")),
                "rule": em.group("rule"),
                "msg": em.group("msg").strip(),
                "help": "",
            })
            i += 1
            continue

        i += 1

    return findings


# ---- output formatter (TSV) ------------------------------------------

_SEV_LETTER = {"error": "E", "warning": "W", "info": "I", "note": "N",
               "style": "S", "convention": "C", "refactor": "R", "help": "H"}
_SEV_RANK = {"error": 4, "warning": 3, "info": 2, "style": 1, "note": 1,
             "convention": 1, "refactor": 1, "help": 0}


def _emit_tsv(findings: List[dict]) -> str:
    """Group by rule, sort errors-first, emit TSV. One help row per
    finding that has one, marked `-` in severity column."""
    if not findings:
        return ""

    # Group by rule for cap + sort.
    groups: dict[str, List[dict]] = {}
    rule_sev: dict[str, str] = {}
    for f in findings:
        groups.setdefault(f["rule"], []).append(f)
        cur = rule_sev.get(f["rule"], "")
        if _SEV_RANK.get(f["sev"], 0) > _SEV_RANK.get(cur, 0):
            rule_sev[f["rule"]] = f["sev"]

    ordered = sorted(groups.items(),
                     key=lambda kv: (-_SEV_RANK.get(rule_sev[kv[0]], 0),
                                     -len(kv[1]),
                                     kv[0]))

    out: List[str] = []
    for rule, items in ordered:
        kept = items if MAX_PER_RULE == 0 else items[:MAX_PER_RULE]
        for f in kept:
            loc = f"{f['file']}:{f['line']}"
            if f["col"]:
                loc += f":{f['col']}"
            sev_letter = _SEV_LETTER.get(f["sev"], f["sev"][:1].upper() or "?")
            out.append(f"{sev_letter}\t{loc}\t{rule}\t{f['msg']}")
            if f.get("help"):
                out.append(f"-\t{loc}\t{rule}\thelp: {f['help']}")
        elided = len(items) - len(kept)
        if elided > 0:
            out.append(f"-\t\t{rule}\t+{elided} more {rule}")

    return "\n".join(out) + "\n"


# ---- main entry: distill or passthrough ------------------------------

def compress(raw: str) -> str:
    try:
        cleaned = ANSI_RE.sub("", raw)

        if _is_verbose(cleaned):
            findings = _parse_verbose(cleaned)
        else:
            # Single-line shapes only. Try to parse but if the result
            # would be larger, the byte-count check at the end falls
            # us back to passthrough.
            findings = []
            for line in cleaned.splitlines():
                f = _terse_parse(line.rstrip())
                if f:
                    findings.append(f)

        if not findings:
            return raw

        distilled = _emit_tsv(findings)

        # Monotonic guarantee: never grow the output. If distillation
        # didn't shrink the bytes, return the original verbatim.
        if len(distilled.encode("utf-8")) >= len(raw.encode("utf-8")):
            return raw
        return distilled
    except Exception:
        return raw


def main() -> int:
    sys.stdout.write(compress(sys.stdin.read()))
    return 0


if __name__ == "__main__":
    sys.exit(main())
