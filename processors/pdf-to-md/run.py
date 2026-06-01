#!/usr/bin/env python3
"""pdf-to-md: convert a PDF on stdin to GitHub-flavored markdown on
stdout.

Strategy (per ADR 0007):
  - Document-level pass via pypdf for metadata + outline → TOC.
  - Per-page pass via pdfplumber for text + tables.
  - Per-page strategy:
      * substantive text → emit markdown for the page
      * empty / gibberish text → flag scanned/OCR placeholder (v0.8)
      * mostly-empty + images present → flag chart/vision placeholder (v0.9)
  - Tables go out as GHFM tables.
  - Images surface as `[image: page N — vision not yet supported]`
    placeholders for now.

Non-destructive: input that doesn't parse as a PDF returns as a
single error line + the first 200 bytes echoed back so the AI can
see what was there. Exit code is always 0; thlibo's middleware
treats non-zero as fallback-to-original-bytes which would defeat
the point on binary input.
"""

from __future__ import annotations

import io
import re
import sys

# Promote lines that look like section headings into markdown
# headings. We use a numeric-prefix heuristic rather than font-size
# analysis because most structured documents (PRDs, specs, RFCs)
# use "1. Foo" / "2.1 Bar" / "3.2.1 Baz" numbering. Gets ~80% of
# real PDFs structurally readable without per-document tuning.
#
# Constraints, derived from running on real PRDs:
#   - Numeric prefix only (1., 1.1, 1.1.1 — up to 4 levels deep).
#   - Length cap of 60 chars in the title body. Real section
#     headings rarely run longer; numbered list items routinely do.
#   - No internal periods in the title body. Numbered list items
#     ("1. Foo. Bar.") are full prose with sentence punctuation;
#     section headings ("1. Executive Summary") are not. This
#     filters out the most common false positive.
#   - Title body must START with an UPPERCASE letter. Filters the
#     most common false positive: when pdfplumber merges table cells
#     into a single text line ("03 the ATC dashboard, so that I..."
#     where 03 was a US-ID prefix in a user-story table).
#   - Section number prefix can't have leading zeros. Real headings
#     are "1." not "01."; "01" / "02" / "03" patterns appear in
#     user-story IDs and other ID-shaped table content.
_HEADING_RE = re.compile(
    r"^(?P<dots>[1-9]\d*(?:\.\d+){0,3})\.?\s+(?P<text>[A-Z][^.\n]{0,59})$"
)

# Preserve LF on Windows: Python's default text-mode stdout
# translates \n -> \r\n which corrupts byte-identity for downstream
# tools that compare bytes. Same fix every Python processor uses.
#
# Force UTF-8 too: Python on Windows defaults stdout to the legacy
# ANSI code page (cp1252), which raises UnicodeEncodeError on common
# PDF text like → em-dashes, and smart quotes — crashing the
# processor and forcing the pipeline to fall back to the raw PDF.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", newline="")


def emit_error(msg: str, raw: bytes) -> None:
    """Best-effort fallback when the PDF can't be parsed.

    Prints a short error block plus a hex preview of the first 32
    bytes so the AI assistant can see what kind of file this
    actually was. Exit code stays 0.
    """
    sys.stdout.write(f"<!-- pdf-to-md: {msg} -->\n")
    preview = raw[:32]
    hexdump = " ".join(f"{b:02x}" for b in preview)
    sys.stdout.write(f"<!-- first 32 bytes: {hexdump} -->\n")


def promote_headings(text: str) -> str:
    """Walk the page's extracted text and promote numbered-section
    lines to markdown headings. Each line is examined independently;
    non-matching lines pass through unchanged.

    Heading depth follows the numeric prefix:
      "1. Foo"        -> "## 1. Foo"     (top level inside the page)
      "1.1 Bar"       -> "### 1.1 Bar"
      "1.1.1 Baz"     -> "#### 1.1.1 Baz"

    The page already has a "## Page N" wrapper, so we start at H2 for
    top-level numbered sections — this means the toplevel-page and
    the toplevel-section land at the same depth in the output. That
    looks slightly redundant when they're adjacent but reads
    correctly when section content follows.
    """
    if not text:
        return text
    out_lines: list[str] = []
    for line in text.split("\n"):
        stripped = line.strip()
        if not stripped:
            out_lines.append(line)
            continue
        m = _HEADING_RE.match(stripped)
        if not m:
            out_lines.append(line)
            continue
        # Depth = number of dots in the numeric prefix + 2 (so "1." is
        # H2, "1.1" is H3, ...). Cap at H6 even though _HEADING_RE
        # only matches up to 4 levels.
        dots = m.group("dots")
        depth = min(2 + dots.count("."), 6)
        out_lines.append(f"{'#' * depth} {stripped}")
    return "\n".join(out_lines)


def page_strategy(text: str, has_images: bool) -> str:
    """Classify a page based on what extract_text + page.images returned.

    Returns one of: "text", "scanned", "chart", "blank".
    """
    stripped = (text or "").strip()
    if not stripped:
        if has_images:
            # Could be scanned (one big image per page) or chart-y
            # (small images embedded). Without a vision pass we
            # can't tell them apart cheaply; both deserve OCR or
            # vision attention. Default to scanned label since OCR
            # (v0.8) is the next thing to ship.
            return "scanned"
        return "blank"
    # Heuristic: if extract_text returned something but it's
    # almost entirely whitespace/punctuation, the page may be
    # mostly chart-y with sparse axis labels.
    alpha = sum(1 for c in stripped if c.isalpha())
    if alpha < 20 and has_images:
        return "chart"
    return "text"


def render_table(table: list[list[str | None]]) -> str:
    """Render a pdfplumber-extracted table as a GitHub-flavored
    markdown table. Empty cells become an empty string; pipes inside
    cells are escaped so they don't break the table structure.
    """
    if not table or not table[0]:
        return ""
    n_cols = max(len(row) for row in table)

    def cell(v: str | None) -> str:
        if v is None:
            return ""
        return str(v).replace("|", r"\|").replace("\n", " ").strip()

    header = [cell(c) for c in table[0]]
    while len(header) < n_cols:
        header.append("")
    sep = ["---"] * n_cols
    body_rows = []
    for row in table[1:]:
        cells = [cell(c) for c in row]
        while len(cells) < n_cols:
            cells.append("")
        body_rows.append("| " + " | ".join(cells) + " |")
    out = [
        "| " + " | ".join(header) + " |",
        "| " + " | ".join(sep) + " |",
    ]
    out.extend(body_rows)
    return "\n".join(out)


def render_outline(reader) -> str:  # noqa: ANN001  (pypdf type stubs)
    """Walk pypdf's outline (bookmarks) and render as nested markdown
    headings or a TOC. Returns empty string if no outline.
    """
    try:
        outline = reader.outline
    except Exception:  # noqa: BLE001  pypdf raises various concrete types
        return ""
    if not outline:
        return ""

    lines: list[str] = []

    def walk(items, depth: int) -> None:  # noqa: ANN001
        for item in items:
            if isinstance(item, list):
                walk(item, depth + 1)
                continue
            try:
                title = str(getattr(item, "title", "") or "").strip()
            except Exception:  # noqa: BLE001
                title = ""
            if not title:
                continue
            indent = "  " * depth
            lines.append(f"{indent}- {title}")

    walk(outline, 0)
    if not lines:
        return ""
    return "## Outline\n\n" + "\n".join(lines) + "\n"


def main() -> int:
    raw = sys.stdin.buffer.read()
    if not raw.startswith(b"%PDF-"):
        emit_error("input does not look like a PDF (no %PDF- magic)", raw)
        return 0

    # Imports inside main so a missing dep produces a clean error
    # message rather than a stack trace at module-load time.
    try:
        import pypdf
        import pdfplumber
    except ImportError as e:
        emit_error(
            f"dependency missing: {e.name}; install with `pip install -r ~/.thlibo/processors/pdf-to-md/requirements.txt`",
            raw,
        )
        return 0

    # Document-level pass (metadata + outline).
    md_parts: list[str] = []
    try:
        reader = pypdf.PdfReader(io.BytesIO(raw))
    except Exception as e:  # noqa: BLE001  pypdf raises various exception types
        emit_error(f"pypdf failed to open document: {e}", raw)
        return 0

    metadata = getattr(reader, "metadata", None) or {}
    # pypdf returns metadata as a dict-like or sometimes a custom
    # object; .get(...) is the safest accessor we have. Both keys
    # are conventional but optional in PDF spec — many real PDFs
    # ship with neither.
    # Common placeholder values writers leave in PDF metadata when
    # the user didn't fill the field. We treat these as empty so
    # the output doesn't lead with "# (anonymous)" or list "- author:
    # untitled" lines that carry no real signal.
    _META_PLACEHOLDERS = {
        "(anonymous)", "anonymous", "untitled", "(untitled)",
        "unknown", "(unknown)", "user", "(user)",
    }

    def _meta(key: str) -> str:
        try:
            v = metadata.get(key) if metadata else None
        except Exception:  # noqa: BLE001  pypdf metadata accessors are inconsistent
            v = None
        s = str(v).strip() if v else ""
        if s.lower() in _META_PLACEHOLDERS:
            return ""
        return s

    title = _meta("/Title")
    author = _meta("/Author")
    n_pages = len(reader.pages)

    if title:
        md_parts.append(f"# {title}")
    front_matter: list[str] = []
    if author:
        front_matter.append(f"- author: {author}")
    front_matter.append(f"- pages: {n_pages}")
    md_parts.append("\n".join(front_matter))

    outline_md = render_outline(reader)
    if outline_md:
        md_parts.append(outline_md)

    # Per-page pass.
    try:
        pdf = pdfplumber.open(io.BytesIO(raw))
    except Exception as e:  # noqa: BLE001
        emit_error(f"pdfplumber failed to open document: {e}", raw)
        sys.stdout.write("\n\n".join(md_parts))
        sys.stdout.write("\n")
        return 0

    page_blocks: list[str] = []
    text_pages = 0
    with pdf:
        for i, page in enumerate(pdf.pages, start=1):
            text = page.extract_text() or ""
            try:
                tables = page.extract_tables() or []
            except Exception:  # noqa: BLE001
                tables = []
            try:
                has_images = bool(page.images)
            except Exception:  # noqa: BLE001
                has_images = False

            strategy = page_strategy(text, has_images)
            block: list[str] = [f"## Page {i}"]
            if strategy == "text":
                text_pages += 1
                block.append(promote_headings(text.strip()))
                if tables:
                    for j, t in enumerate(tables, start=1):
                        rendered = render_table(t)
                        if rendered:
                            block.append(f"### Table {i}.{j}")
                            block.append(rendered)
                if has_images:
                    block.append(
                        f"_[image{'s' if len(page.images) > 1 else ''} on page {i} — vision not yet supported]_"
                    )
            elif strategy == "scanned":
                block.append(
                    f"_[scanned page {i} — OCR not yet supported]_"
                )
            elif strategy == "chart":
                block.append(
                    f"_[chart on page {i} — vision not yet supported]_"
                )
                if text.strip():
                    # Surface what little text we did get; might be
                    # axis labels or a caption.
                    block.append("```")
                    block.append(text.strip())
                    block.append("```")
            else:  # blank
                block.append("_[blank page]_")
            page_blocks.append("\n\n".join(block))

    md_parts.extend(page_blocks)

    # Low-value sentinel. If no page produced extractable text, the
    # output is just placeholders ("scanned page N — OCR not yet
    # supported") and the consumer can't do anything with it. Emit a
    # marker on the LAST line so casefile.Create can detect this and
    # bail out — letting Claude Code's native multimodal PDF reader
    # take over instead of feeding it 200 bytes of "OCR not supported".
    # See issue #31. The sentinel format is intentionally noisy so
    # nothing else accidentally produces it.
    if n_pages > 0 and text_pages == 0:
        md_parts.append("<!-- thlibo-pdf-low-value: no extractable text -->")

    sys.stdout.write("\n\n".join(md_parts))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
