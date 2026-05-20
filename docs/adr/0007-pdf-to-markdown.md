# 0007. PDF → Markdown converter (pypdf + pdfplumber, Python script processor)

- Status: accepted
- Date: 2026-05-20

## Context

Users dropping PDFs into Claude Code (via `Read foo.pdf` or attaching
to the conversation) want the AI to actually understand the
content. Today the Read tool serves the binary PDF bytes; the model
sees a blob of header magic and PDF stream operators. Useless.

We want a processor that converts a PDF to markdown — lossless on
text, structurally faithful for tables, descriptive on images
(eventually) — and slots into thlibo's existing case-file pipeline
the same way the log filters do.

The decision space splits along three axes:

1. **What library** — Python (`pypdf` + `pdfplumber`), Go
   (`pdfcpu`), Node (`pdf.js`), or something else.
2. **What architecture** — script processor, prompt processor,
   subcommand, hybrid.
3. **What to do about images** — skip them, OCR everything, vision-
   model-describe charts, page-family-adaptive.

## Decision

**Library: Python `pypdf` + `pdfplumber`.** Both MIT-licensed, both
pure-Python, both already trivial pip installs. `pypdf` handles
document-level operations (metadata, outline → markdown TOC, page
count, simple text extraction). `pdfplumber` handles the per-page
heavy lifting: positioned text items for layout-aware extraction,
first-class table reconstruction (`page.extract_tables()`), and
access to embedded image objects.

`pdfcpu` was considered as a pure-Go alternative, but it doesn't
expose a text-extraction API — only image/font/page splitting. It
would let us avoid the Python dependency on the PDF path, but
matching pdfplumber's table quality with raw content-stream parsing
is research-grade work. Out of scope for v0.7.

`pdf.js` was considered for parity with Mozilla's reference PDF
renderer. It would commit thlibo to bundling a Node.js runtime —
50 MB extra footprint to gain capabilities we already have via
Python. Wrong tradeoff for a CLI tool.

`PyPDF2` was considered briefly but is deprecated; it merged into
`pypdf` in 2022. Anything that was in PyPDF2 is in pypdf plus more.

**Architecture: script processor at `processors/pdf-to-md/`.** Slots
into thlibo's existing processor model (yaml descriptor + Python
entry script + match regex). Triggered by:

- The Read PreToolUse hook, when `tool_input.file_path` ends in
  `.pdf` — converted output gets piped through `thlibo case` to a
  case-file directory, and Claude Code reads the markdown instead
  of the binary.
- A new `thlibo pdf <input.pdf>` subcommand for explicit ad-hoc use:
  `thlibo pdf foo.pdf > foo.md`, `thlibo pdf foo.pdf | thlibo
  shorthand --in-place -`.

Both paths share the same `processors/pdf-to-md/run.py` body. The
hook calls into it via the case-file pipeline; the subcommand calls
it directly via stdin/stdout. The deterministic-Python parts run
without inferd.

**Image strategy: page-family adaptive, vision deferred.** Each page
gets classified by `pdfplumber.extract_text()` output:

- Substantive text returned → text path; skip images for now,
  surface as `[image: <filename> on page N]` placeholder. ~80 % of
  real-world PDFs land here (technical docs, papers, manuals,
  contracts).
- Empty / gibberish text + non-empty `page.images` → flag for OCR
  in v0.8 (pytesseract). Output `[scanned page N — OCR not yet
  supported]` until then.
- Mostly-empty text + images that look chart-shaped → flag for
  vision in v0.9 (requires inferd vision endpoint, currently
  text-only). Output `[chart on page N — vision not yet supported]`
  until then.

This staged rollout lets v0.7 ship the most-common path with zero
inferd dependency. OCR and vision are additive.

## Consequences

**Easier:**

- Per-page strategy means a 60-page PDF that's mostly-prose-with-
  one-table extracts cleanly from the first commit. The two pages
  with charts get placeholder text; the user can revisit those
  manually if they matter.
- Python is already a thlibo runtime requirement (the existing
  log-family filters are all Python). Adding `pypdf` + `pdfplumber`
  to `~/.thlibo/processors/pdf-to-md/requirements.txt` is the same
  install cost users already pay for the other processors.
- The case-file pipeline already handles "convert one input file to
  a directory of artefacts" — `compressed.md`, `meta.json`,
  `summary.md`. The PDF processor reuses that shape: input PDF →
  case dir with `compressed.md` (the converted markdown), `meta.json`
  (page count, table count, image count, source SHA), `summary.md`
  (one-line description Claude can use for context).
- Tables come out as GitHub-flavored markdown tables; nested tables
  flatten cleanly via pdfplumber's row/cell model.

**Harder:**

- Three Python dependencies to install (`pypdf`, `pdfplumber`,
  `pdfminer.six` as pdfplumber's transitive). Adds ~5 MB to a fresh
  pip install and pulls in `Pillow` if image extraction is enabled.
- Per-page strategy decisions can be wrong. A page with a
  text-rendered chart caption that pdfplumber happily extracts will
  *not* be flagged for vision treatment, even though the chart
  itself contains the actual data the user cares about. Acceptable
  for v0.7 since the ground truth is "we tried text extraction and
  it returned something"; pages where that's the wrong choice will
  surface as user feedback.
- OCR (v0.8) requires `tesseract` as a system binary, not just a
  pip package. Soft dep: if `pytesseract` import fails or the
  `tesseract` binary isn't on PATH, scanned pages stay as
  placeholder text and the install instructions surface.
- Vision (v0.9) is gated on inferd's protocol-v2 (or a v1 additive
  field) — `inferd.Request.Messages` is currently text-only
  `{Role, Content}` strings. Adding image content to a request is
  an upstream conversation we haven't started.

**Reversible if needed:**

- Swapping `pypdf` for `pymupdf` later is a single-file change
  (the entry script) if pypdf's text quality turns out worse. We
  avoided pymupdf's AGPL license today; if Mozilla relicenses or
  we get clearance to ship AGPL deps, the better text quality
  could be worth it.
- Swapping `pdfplumber` for `marker` or `markitdown` (the
  Microsoft tool) is a similar single-file change. We avoided
  markitdown today because it bundles too much (Office doc
  handling we don't want), but the text-extraction core is
  swappable.

## Implementation order

The processor lands in three releases:

- **v0.7.0** (next): text + tables. No OCR, no vision. The 80 %
  case. ~150 lines of Python + the yaml descriptor + the case-file
  glue + the `thlibo pdf` subcommand. Tests against a fixture set
  of representative PDFs (born-digital prose, two-column academic,
  table-heavy spec, scanned PDF-as-placeholder).
- **v0.8.0**: OCR for scanned pages via `pytesseract`. Detection
  per-page; users without `tesseract` installed get a clear error
  message pointing at install instructions for their platform.
- **v0.9.0**: vision-model image descriptions via inferd's vision
  endpoint. Requires inferd protocol updates — separate ADR + RFC
  in inferd.

## References

- pypdf: https://pypdf.readthedocs.io/ (BSD-3, active)
- pdfplumber: https://github.com/jsvine/pdfplumber (MIT, active)
- pdfcpu: https://github.com/pdfcpu/pdfcpu (Apache-2.0; image/page
  splitting, no text extraction API)
- pdf.js: https://mozilla.github.io/pdf.js/ (MIT; rejected for CLI
  due to Node runtime weight)
- markitdown: https://github.com/microsoft/markitdown (MIT; rejected
  for bundling unrelated Office handlers)
- pymupdf: https://github.com/pymupdf/PyMuPDF (AGPL; rejected for
  license posture)
- Existing case-file pipeline: `internal/casefile/`
- Existing Python processors as reference shape:
  `processors/git-filter/`, `processors/pytest-filter/`,
  `processors/ndjson-filter/`
