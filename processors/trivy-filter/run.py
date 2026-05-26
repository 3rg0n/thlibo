#!/usr/bin/env python3
"""trivy-filter: distill Trivy's box-drawing tables into TSV.

Trivy's default output is a series of unicode box-drawing tables, one
per scanned target. Each row is enclosed in `│ ... │`, and a single
finding can span multiple visual rows when its title or fixed-version
list wraps. Logical rows are separated by `├──...──┤` separators.

This processor:
  - parses the `Library / Vulnerability / Severity / Status /
    Installed Version / Fixed Version / Title` table shape
  - merges continuation rows (empty cells inherit the prior value)
  - emits one TSV line per CVE: sev-letter \\t lib:installed \\t CVE
    \\t fixed \\t title (URL-suffix dropped from the title cell)
  - groups the findings by severity, errors-first
  - falls back to raw bytes on any error or whenever the distilled
    output would not be smaller (monotonic guarantee)

Output schema (5 columns, tab-separated):
    severity \\t lib@installed \\t CVE \\t fixed-version \\t title

severity letter: C=critical, H=high, M=medium, L=low, U=unknown.

Skips Trivy's `Report Summary` and `Total: ...` banner lines, plus
the `Legend:` and any `Type: -` rows.
"""

from __future__ import annotations

import os
import re
import sys
from typing import List, Optional

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="", encoding="utf-8")
if hasattr(sys.stdin, "reconfigure"):
    sys.stdin.reconfigure(encoding="utf-8", errors="replace")

ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")

# A trivy table row has at least 3 vertical bars: `│ a │ b │`.
_ROW_RE = re.compile(r"^\s*│(.*)│\s*$")
# A separator row contains horizontal box-drawing characters (`─`)
# anywhere along with junction chars. Trivy's "partial separators" can
# start with `│ ` for the column that hasn't changed and `├`/`┤` for
# the columns that have, e.g. `│   ├──┤   │   ├──┼──┤`.
_SEP_RE = re.compile(r"[─┬┼┴]")
_FULL_SEP_RE = re.compile(r"^\s*[├┌└][─┬┼┴┤┐┘]+[┤┐┘]\s*$")
# A header row (we want to skip but use to detect column meaning).
_HEADER_RE = re.compile(
    r"Library.*Vulnerability.*Severity.*(?:Status.*)?Installed Version.*Fixed Version.*Title",
    re.IGNORECASE,
)
# A target/banner line above the table.
_TARGET_RE = re.compile(r"^(?P<target>\S.+?)\s+\(\S+\)\s*$")
_TOTAL_RE = re.compile(r"^Total:\s+\d+\s+\(", re.IGNORECASE)

# CVE ID pattern (also matches GHSA, CGA, etc.)
_VULN_ID_RE = re.compile(r"^(?:CVE-\d{4}-\d+|GHSA-[\w-]+|CGA-[\w-]+|[A-Z]+-\d+(?:-\d+)?)$")

# URLs printed inside the Title cell — we drop them since the CVE ID
# already locates the advisory.
_URL_RE = re.compile(r"\bhttps?://\S+")

_SEV_LETTER = {
    "critical": "C",
    "high": "H",
    "medium": "M",
    "low": "L",
    "unknown": "U",
}
_SEV_RANK = {
    "critical": 5,
    "high": 4,
    "medium": 3,
    "low": 2,
    "unknown": 1,
}


def _split_cells(line: str) -> List[str]:
    """Split a `│ a │ b │ c │` row into its cell strings, stripped."""
    m = _ROW_RE.match(line)
    if not m:
        return []
    inner = m.group(1)
    parts = inner.split("│")
    return [p.strip() for p in parts]


def _is_table_row(line: str) -> bool:
    # A data row contains `│` but no horizontal-box-drawing characters.
    if "│" not in line:
        return False
    return "─" not in line


def _is_separator(line: str) -> bool:
    # Anything inside the table that contains `─` is a separator
    # (full or partial — partial separators start with `│   ├──┤` etc).
    return "─" in line and ("│" in line or "├" in line or "└" in line or "┌" in line)


def _parse_table(lines: List[str], start: int) -> tuple[List[dict], int]:
    """Parse a table starting at `lines[start]` (assumed to be the
    `┌──┐` opener). Returns (findings, next-index-after-table).

    Each finding dict carries: lib, vuln, sev, status, installed,
    fixed, title.
    """
    n = len(lines)
    i = start

    # Walk forward to find the header row to learn the column count.
    header_cells: List[str] = []
    while i < n:
        if _is_table_row(lines[i]):
            cells = _split_cells(lines[i])
            joined = " | ".join(cells)
            if _HEADER_RE.search(joined):
                header_cells = [c.lower() for c in cells]
                i += 1
                break
        if _is_separator(lines[i]) or _is_table_row(lines[i]):
            i += 1
            continue
        # Not a table line at all — bail.
        return [], start + 1
    if not header_cells:
        return [], i

    # Map column index → semantic name.
    col_idx = {}
    for idx, name in enumerate(header_cells):
        if "library" in name:
            col_idx["lib"] = idx
        elif "vulnerability" in name:
            col_idx["vuln"] = idx
        elif "severity" in name:
            col_idx["sev"] = idx
        elif "status" in name:
            col_idx["status"] = idx
        elif "installed" in name:
            col_idx["installed"] = idx
        elif "fixed" in name:
            col_idx["fixed"] = idx
        elif "title" in name:
            col_idx["title"] = idx

    # We need at least lib/vuln/sev/title to do useful work.
    required = ("lib", "vuln", "sev", "title")
    if any(k not in col_idx for k in required):
        return [], i

    findings: List[dict] = []
    cur: Optional[dict] = None

    def _flush():
        nonlocal cur
        if cur is not None and cur.get("vuln"):
            findings.append(cur)
        cur = None

    while i < n:
        line = lines[i]
        if _is_separator(line):
            # A separator with `┘` (right-bottom corner) ends the table.
            # `┴` alone is ambiguous: it can appear in a partial inner
            # separator too. Use trailing `┘` as the close marker.
            stripped_end = line.rstrip()
            if stripped_end.endswith("┘"):
                _flush()
                return findings, i + 1
            # Separator BETWEEN rows. If the vuln-column of the
            # following row is blank, it's a wrap continuation;
            # otherwise it's a new finding.
            if i + 1 < n and _is_table_row(lines[i + 1]):
                next_cells = _split_cells(lines[i + 1])
                if len(next_cells) > col_idx["vuln"] and next_cells[col_idx["vuln"]]:
                    _flush()
            i += 1
            continue

        if _is_table_row(line):
            cells = _split_cells(line)
            if len(cells) <= max(col_idx.values()):
                i += 1
                continue
            if cur is None:
                cur = {
                    "lib": cells[col_idx["lib"]],
                    "vuln": cells[col_idx["vuln"]],
                    "sev": cells[col_idx["sev"]],
                    "status": cells[col_idx["status"]] if "status" in col_idx else "",
                    "installed": cells[col_idx["installed"]] if "installed" in col_idx else "",
                    "fixed": cells[col_idx["fixed"]] if "fixed" in col_idx else "",
                    "title": cells[col_idx["title"]],
                }
            else:
                # Continuation: empty cells inherit the prior value;
                # non-empty cells append to the title (the most common
                # wrap target) or replace the column value.
                for key, idx in col_idx.items():
                    if idx >= len(cells):
                        continue
                    val = cells[idx]
                    if not val:
                        continue
                    if key == "title":
                        # Append wrapped title text with a single space.
                        cur[key] = (cur.get(key, "") + " " + val).strip()
                    else:
                        # Other columns: a non-empty cell on a
                        # continuation row means a NEW value (e.g.
                        # severity changed). Replace.
                        if not cur.get(key):
                            cur[key] = val
                        else:
                            # Pre-populated; treat the new value as
                            # the same finding's continuation only
                            # for title. For other columns this means
                            # we hit a new row that wasn't separated
                            # — flush and start fresh.
                            _flush()
                            cur = {
                                "lib": cells[col_idx["lib"]] if "lib" in col_idx and col_idx["lib"] < len(cells) else "",
                                "vuln": cells[col_idx["vuln"]] if "vuln" in col_idx and col_idx["vuln"] < len(cells) else "",
                                "sev": cells[col_idx["sev"]] if "sev" in col_idx and col_idx["sev"] < len(cells) else "",
                                "status": cells[col_idx["status"]] if "status" in col_idx and col_idx["status"] < len(cells) else "",
                                "installed": cells[col_idx["installed"]] if "installed" in col_idx and col_idx["installed"] < len(cells) else "",
                                "fixed": cells[col_idx["fixed"]] if "fixed" in col_idx and col_idx["fixed"] < len(cells) else "",
                                "title": cells[col_idx["title"]] if "title" in col_idx and col_idx["title"] < len(cells) else "",
                            }
                            break
            i += 1
            continue

        # Non-table line inside the table region — skip.
        i += 1

    _flush()
    return findings, i


def _is_table_open(line: str) -> bool:
    """Top border of a table: `┌──...──┐`."""
    s = line.strip()
    return s.startswith("┌") and s.endswith("┐")


def _emit_tsv(findings: List[dict]) -> str:
    if not findings:
        return ""
    # Drop URL noise from titles, propagate inherited fields, then
    # sort by severity (criticals first), library name.
    rows: List[tuple[str, str]] = []  # (sev_rank_key, line)
    last_lib = ""
    last_sev = ""
    last_installed = ""
    for f in findings:
        # Continuations may carry forward the lib/sev/installed cells
        # as blanks — promote.
        lib = f["lib"] or last_lib
        sev_raw = (f["sev"] or last_sev).lower().strip()
        installed = f["installed"] or last_installed
        if lib:
            last_lib = lib
        if sev_raw:
            last_sev = sev_raw
        if installed:
            last_installed = installed
        if not f.get("vuln") or not _VULN_ID_RE.match(f["vuln"]):
            continue
        title = _URL_RE.sub("", f.get("title", "")).strip()
        title = re.sub(r"\s+", " ", title)
        sev_letter = _SEV_LETTER.get(sev_raw, sev_raw[:1].upper() or "?")
        lib_at = f"{lib}@{installed}" if installed else lib
        fixed = f.get("fixed", "").strip() or "-"
        line = f"{sev_letter}\t{lib_at}\t{f['vuln']}\t{fixed}\t{title}"
        rows.append((sev_raw, line))

    rows.sort(key=lambda kv: (-_SEV_RANK.get(kv[0], 0), kv[1]))
    return "\n".join(line for _, line in rows) + "\n"


def compress(raw: str) -> str:
    try:
        cleaned = ANSI_RE.sub("", raw)
        lines = cleaned.splitlines()

        all_findings: List[dict] = []
        i = 0
        n = len(lines)
        while i < n:
            if _is_table_open(lines[i]):
                # Find the column header (it's the next row).
                fs, j = _parse_table(lines, i)
                if j == i:
                    j = i + 1
                # Skip the small "Report Summary" overview table at
                # the top — it has Target/Type/Vulnerabilities columns,
                # no CVEs.
                if fs:
                    all_findings.extend(fs)
                i = j
                continue
            i += 1

        if not all_findings:
            return raw

        distilled = _emit_tsv(all_findings)
        if not distilled.strip():
            return raw

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
