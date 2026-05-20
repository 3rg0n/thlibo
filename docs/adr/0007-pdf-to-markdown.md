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
- Empty / gibberish text + non-empty `page.images` → scanned page
  placeholder. Output `[scanned page N — OCR not yet supported]`.
- Mostly-empty text + images that look chart-shaped → chart
  placeholder. Output `[chart on page N — vision not yet
  supported]`.

The inferred next step was to add `pytesseract` for scanned-page
OCR (v0.8) and a separate vision-model path for chart description
(v0.9). That staging changed when we learned **Gemma 4 itself
handles OCR via its vision capability** — the model that inferd
already loads for compression can do both. Per Google's developer
docs:

```python
messages = [
    {"role": "user", "content": [
        {"type": "image", "url": img_url},
        {"type": "text", "text": "What does the sign say?"}
    ]}
]
output = vqa_pipe(messages, ...)
```

That collapses v0.8 OCR and v0.9 chart-description into a single
feature: send the page-rendered-as-image to inferd via a
multimodal `Request.Messages`, get back text. **One feature ship,
not two.** It's gated on inferd exposing image content over its
NDJSON wire — currently `inferd.Message` is text-only `{Role,
Content}`. inferd is working on that.

Updated rollout:

- **v0.7**: text + tables, no model. The 80 % case. (This release.)
- **v0.8**: scanned + chart pages get sent to inferd's multimodal
  Gemma 4 with a "transcribe this page" or "describe this chart"
  prompt. Both paths use the same wire endpoint; the prompt is
  what differs. Requires inferd's vision IPC to ship.

This staged rollout lets v0.7 ship the most-common path with zero
inferd dependency. The OCR + vision merger is additive once
inferd's wire supports image payloads.

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

- Swapping `pdfplumber` for `markitdown` (the Microsoft tool) is
  a single-file change in the entry script. We avoided markitdown
  today because it bundles too much (Office doc handling we don't
  want), but the text-extraction core is swappable inside our
  permissively-licensed posture.

## Implementation order

The processor lands in two releases (down from three after the
Gemma-4-OCR insight collapsed v0.8 + v0.9):

- **v0.7.0** (next): text + tables + numeric heading promotion.
  No OCR, no vision. The 80 % case. Pure-Python deterministic
  path; no inferd dependency. Verified end-to-end against a real
  29-page Cisco PRD and a 19-page MIME-info spec.
- **v0.8.0**: scanned + chart pages render as images and get sent
  to inferd's multimodal Gemma 4 with a transcription / chart-
  description prompt. Both paths share the same wire endpoint;
  only the prompt differs. Gated on inferd exposing image payloads
  over its NDJSON wire (in flight).

## References

- pypdf: https://pypdf.readthedocs.io/ (BSD-3, active)
- pdfplumber: https://github.com/jsvine/pdfplumber (MIT, active)
- pdfcpu: https://github.com/pdfcpu/pdfcpu (Apache-2.0; image/page
  splitting, no text extraction API)
- pdf.js: https://mozilla.github.io/pdf.js/ (MIT; rejected for CLI
  due to Node runtime weight)
- markitdown: https://github.com/microsoft/markitdown (MIT; rejected
  for bundling unrelated Office handlers — the text-extraction
  core is swappable later if useful)

### Considered and excluded for license posture

thlibo ships under MIT and we keep the dependency tree
permissively licensed end-to-end. Tools below are notable in this
space but excluded from both the built-in path and any
documented user-recipe — we don't surface installation paths for
copyleft deps even as opt-in, because doing so puts us in the
position of recommending license obligations users may not want.
Listed here for completeness so future readers don't think we
missed them:

- pymupdf (AGPL-3.0)
- marker (GPL-3.0; also multi-GB ML runtime, separate concern)
- mupdf-based forks more generally (the upstream is AGPL)
- Gemma 4 vision/OCR: https://ai.google.dev/gemma/docs/capabilities/vision
  (the model inferd already loads for compression is multimodal-
  capable; v0.8 PDF OCR + chart description rides this once
  inferd exposes image payloads over its wire)
- Existing case-file pipeline: `internal/casefile/`
- Existing Python processors as reference shape:
  `processors/git-filter/`, `processors/pytest-filter/`,
  `processors/ndjson-filter/`
