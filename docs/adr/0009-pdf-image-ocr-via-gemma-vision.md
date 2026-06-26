# 0009. Scanned-PDF OCR via Gemma vision, dispatched Go-side

- Status: accepted
- Date: 2026-06-25
- Supersedes the scanned-PDF deferral in [0007](0007-pdf-to-markdown.md)
  (which flagged image-only pages as `[scanned page N — OCR not yet
  supported]` placeholders pending "v0.8 pytesseract").

## Context

ADR 0007 shipped `pdf-to-md`: a Python script processor that extracts
born-digital PDF text well, but flags image-only (scanned) pages as
placeholders. For a scanned legal deed, thlibo therefore returned only
`_[scanned page N — OCR not yet supported]_` — zero usable content. The
`#31` low-value path (casefile detects the placeholder sentinel, exits
`ExitLowValue`, lets Claude's native reader take over) made that
graceful, but thlibo itself surfaced nothing.

Two things changed since 0007:

1. **inferd v0.4+ ships a multimodal vision path** (their ADRs
   0015/0016/0021): a v2 request may carry image attachments (raw
   decoded RGB; the daemon links no image codec), which the llamacpp
   backend feeds to Gemma 4 via the `mtmd`/CLIP projector. inferd v0.5
   auto-loads the projector.
2. **thlibo now owns its inferd wire codec** (`internal/inferd`) and we
   implemented the attachment/BLOB path against `protocol-v2.md`
   §3.5/§3.7.

A spike (deed page → raw RGB → image attachment → live inferd v0.5
vision daemon, through thlibo's own codec over IPC) returned a
near-perfect transcription: all parties, covenant clauses, three
schedules, and the property address. The capability is real.

Two decisions follow: **which OCR engine**, and **where the image
dispatch lives**.

## Decision

**Engine: Gemma vision, not Tesseract.** ADR 0007 assumed Tesseract.
We choose Gemma-4 vision over inferd instead:

- It reuses the already-warm model — no second engine, no new native
  dependency (Tesseract is a C++ install-and-PATH burden across Win/
  macOS/Linux, exactly the cross-platform pain inferd was created to
  absorb).
- It handles layout/structure, not just glyphs — the spike preserved
  the deed's schedule headings and clause structure as Markdown.
- It keeps thlibo's "inference is inferd's job" invariant (ADR 0005)
  intact: thlibo rasterizes and dispatches; inferd does the model work.

Trade-off: a vision pass is slower and non-deterministic vs. Tesseract,
and requires a vision-capable daemon (projector loaded). Both are
acceptable because the path is gated by capability detection and
fail-open (below).

**Dispatch: Go-side, not in the Python processor.** The `pdf-to-md`
processor is a stdin→stdout *text* script; it cannot carry raw RGB
bytes or speak inferd's binary wire. Rather than fatten the processor
with a duplicate IPC client (breaking the "processors are simple text
filters" invariant), the **Go middleware owns the image dispatch**:

- `pdf-to-md` keeps emitting its existing per-page output, including the
  `<!-- thlibo-pdf-low-value: … -->` sentinel for image-only documents.
- `casefile.Create` already detects that sentinel (the `#31` hook).
  When detected AND a vision-capable inferd is reachable, the Go side
  rasterizes the source PDF's pages to RGB and sends each as an
  `internal/inferd` image attachment with an OCR prompt, replacing the
  placeholder output with the real transcription.
- Rasterization uses the same Python the processor already depends on
  (pdfplumber/pypdfium2) invoked as a render-only step, OR a Go PDF
  rasterizer — resolved at implementation time; the dispatch boundary
  (Go owns it) is the load-bearing decision here.

**Fail-open is the gate (lazy, not a pre-probe).** The OCR path is
best-effort, and the gating is discovered by *attempting* the call
rather than by pre-querying a capabilities frame:

- If inferd is unreachable, or the active backend has no vision
  projector loaded, the image request fails (connect error, or a clean
  daemon-side rejection of the attachment). thlibo catches that and
  keeps today's `#31` behaviour exactly — low-value passthrough,
  Claude's native reader handles the original. No hard failure, ever
  (ADR 0006).
- A page that fails to rasterize or whose vision call errors falls back
  to the placeholder for that page; partial transcription is still a
  win over none.

We deliberately do **not** add a separate capabilities pre-probe in
v1: the generation socket carries no capability query on thlibo's
client surface, and the lazy "try and fall open" path is functionally
equivalent for the user (correct fallback, no crash) at the cost of one
wasted rasterize+dispatch when vision is absent. A pre-probe (e.g. via
the admin socket) is a possible later optimisation, not a correctness
requirement — recorded here so the absence is a decision, not an
oversight. The bounded-resource guarantees below hold regardless.

## Consequences

**Easier:**
- Scanned PDFs return real content through thlibo for the first time.
- No new external engine/dependency; reuses inferd + the warm model.
- The dispatch lives where the wire codec already is; the processor
  stays a dumb text filter.

**Harder / costs:**
- Vision inference is slower and token-heavier than text extraction;
  large scanned docs cost real wall-clock + tokens. Mitigate with a
  page cap / size guard at implementation time.
- Output is non-deterministic and can mis-OCR (the spike read "Castle"
  as "Castie"). Acceptable for a read-assist; not for anything that
  round-trips to disk as authoritative.
- Requires a vision-capable daemon; older/projector-less inferd falls
  back to placeholders (capability-gated, so not a break).

**Reversible:**
- The engine choice is isolated behind the Go dispatch. Swapping in
  Tesseract later (e.g. for an offline/deterministic mode) changes one
  package; the casefile sentinel hook and fail-open contract don't move.

## References

- ADR 0005 (inference is inferd's job), ADR 0006 (fail open),
  ADR 0007 (pdf-to-md; scanned-page deferral superseded here).
- inferd `protocol-v2.md` §3.5/§3.7 (attachments + BLOB frames),
  inferd ADR 0016 (consumer decodes media), ADR 0021 (v2 wire).
- Issue #31 (low-value passthrough — the casefile hook this builds on),
  #34 (the feature this implements).
- Spike: deed page OCR via `internal/inferd` attachment path against
  live inferd v0.5 vision daemon (2026-06-25).
