# 0015. Native Go PDF extraction (vendored gopdf), replacing the Python primary path

- Status: accepted
- Date: 2026-08-02
- Supersedes the library and architecture decisions in
  [0007](0007-pdf-to-markdown.md); its OCR staging stands via
  [0009](0009-pdf-image-ocr-via-gemma-vision.md)

## Context

ADR 0007 chose Python (`pypdf` + `pdfplumber`) for PDF → markdown on an
explicit premise:

> `pdfcpu` was considered as a pure-Go alternative, but it doesn't expose a
> text-extraction API — only image/font/page splitting. […] matching
> pdfplumber's table quality with raw content-stream parsing is
> research-grade work. Out of scope for v0.7.

The `pdfcpu` half of that is still true and still verified (its text-extract
issue #122 has been open since 2019-10-29). The "research-grade" half is
what we went and measured, and it does not survive contact with the numbers.

Three things changed since 0007:

1. **ADR 0010 moved every other deterministic built-in to native Go.** It
   explicitly held `pdf-to-md` and `cordon-filter` back as Python. `pdf-to-md`
   is now the *only* reason a PDF read pays an interpreter launch.
2. **We surveyed the Go options properly.** `go-fitz` is AGPL (excluded on
   license posture). `klippa/go-pdfium` via wazero is MIT and CGO-free, but
   recovered 2.1% of tables on a 26-page test document. `UniPDF` is
   commercial. `dslipak/pdf` timed out at 10 minutes on a corpus file.
   `ledongthuc/pdf` lost 0.01% of text but has no table model.
   `razvandimescu/gopdf` (MIT, zero deps, no CGO) has both text *and* a
   geometry table model.
3. **The measurement inverted the premise.** Against pdfplumber on the same
   corpus, gopdf mangled *less* text (0.11% vs 6.39%) and ran two orders of
   magnitude faster.

Head-to-head on five representative corpus documents, native Go vs
`processors/pdf-to-md/run.py`, same machine:

| document | in | Go | Python | speedup |
|---|---|---|---|---|
| `2404.16130v2.pdf` | 6.89 MB | 30 ms → 103033 B | 2893 ms → 87623 B | 96× |
| `Cryptlib-3.4.8-Product-Manual.pdf` | 3.03 MB | 302 ms → 622573 B | 38411 ms → 1097611 B | 127× |
| `s00766-024-00432-3.pdf` | 2.74 MB | 130 ms → 170706 B | 7775 ms → 157492 B | 60× |
| `52274.pdf` | 133 KB | 5 ms → 18716 B | 650 ms → 17327 B | 130× |
| `report.pdf` | 193 KB | 51 ms → 30451 B | 1556 ms → 33015 B | 31× |

Aggregate: **51.3 s → 0.52 s (99×)**. That is not a micro-optimisation. A
38-second hook is a hook the user experiences as a hang; a 302 ms one is
invisible. Output sizes land within ±15% either way, so the speed is not
bought with content.

## Decision

**Vendor the gopdf extraction subset into `internal/pdf/` and implement
`pdf-filter` as a native Go processor.** `pdf-to-md` stays installed and
keeps its `^%PDF-` match; it is no longer the primary path.

### Vendored, not required

`internal/pdf/VENDOR.md` carries the full rationale and the complete list of
local changes. In short: this code parses untrusted input, upstream is a
12-star single-maintainer v0.9.x project, and we needed to patch it
immediately. A `require` line gives none of local patching, a frozen review
surface, or subsetting. ~4.3k lines kept, ~1.9k lines of PDF *generation*
dropped — thlibo only ever reads.

### Four tiers, best evidence first

Each tier is used only where it is actually authoritative:

1. **`/StructTreeRoot`** — a Tagged PDF states its own headings, tables and
   reading order. This is the producer telling us the structure rather than
   us inferring it, so it wins outright when present. Adobe's accessibility
   tagging is the same data a screen reader consumes.
2. **Native text extraction** — glyph positions grouped into lines.
3. **Geometry table detection** — recovers tables no one tagged.
4. **Scanned pages** — a `[scanned page N]` placeholder plus a low-value
   sentinel, handed to the existing Gemma-vision OCR path. **This is why
   `pdf-to-md` is retained:** ADR 0009's OCR flow needs page
   *rasterization*, which is not text extraction and which no pure-Go
   library in our license posture provides.

### Tags are model-facing input, so they get verified

A structure tree binds text to tags through marked-content IDs, and thlibo
feeds the result to a model. Two defects here were not cosmetic:

- BDC handling discarded the inline properties dictionary, so every span
  carried `MCID: -1` and nothing bound at all.
- MCIDs are only unique *per page*, and keying on MCID alone collided
  across pages — measured at **13.8× content inflation**, i.e. the same
  prose repeated into the model's context. Fixed by keying on
  `(page, MCID)`.

The lesson generalises: a tier that claims better evidence has to be
verified against the corpus, because a confidently wrong structure tree is
worse output than no structure tree.

### Layout grids are refused, using geometry rather than text

Slide decks and multi-column prose position text with invisible grids that a
geometry detector reads as tables. Text-adjacency heuristics cannot tell a
real column boundary from a mid-word cut, because both look like two
adjacent strings.

Gutters can. **A column boundary is space** — adjacent cells in a real table
are separated by a visible gap, because that gap is how a human reader sees
the columns at all. So we measure it: the distance from the left cell's
rightmost glyph to the right cell's leftmost, compared against the font
size. Over the 41-document corpus, every table whose columns were real
scored 0.00 tight junctions; the paragraph and TOC false positives scored
0.47 to 1.00. **Nothing landed in between**, so the 0.25 threshold sits in
the middle of an empty gap rather than on a judgement call. False-positive
tables on the Cryptlib manual went 13 → 0.

### Fuzzing is part of the deal, not a nice-to-have

Upstream ships 102 tests and zero fuzz targets — defensible for a library
that reads files its own caller wrote. thlibo reads whatever an AI client
was pointed at.

Three fuzz targets found **three independent hangs** in the read path: a
lexer that failed to advance on a stray `)`, an xref subsection whose
declared entry count was trusted (a header of `0 60000000000` spun sixty
billion no-op iterations), and a `/Pages` node naming itself as its own
`/Kids`. Plus one silent-corruption bug: `/Differences` narrowed a
file-controlled code with `byte(v)`, so `[300 /alpha]` aliased onto `0x85`
and overwrote whichever glyph legitimately sat there.

**A hang is strictly worse than a panic under invariant #2.** `RunNative`
recovers a panic and passes the original bytes through, so a crash degrades
to "no compression" — the contract holds. Nothing recovers a hang: the hook
blocks and the AI client waits on it. Every one of these three was in that
class, which is the whole argument for fuzzing a parser we vendored rather
than trusting its test count.

Encrypted documents are refused outright (`/Encrypt` present → pass through
the original bytes), because upstream has no encryption support at all and
would otherwise emit extraction over ciphertext as if it were text.

## Consequences

**Easier:**

- A PDF read costs no interpreter launch, no `pip install`, no `pypdf` /
  `pdfplumber` / `pdfminer.six` / `Pillow`. On the 41-document corpus:
  75.6 MB → 1.90 MB, **97.48% reduction**, no crashes.
- Tagged PDFs get real structure instead of inferred structure.
- The parser is in-tree, so the next defect is ours to fix in an hour
  rather than ours to wait on.

**Harder:**

- ~4.3k lines of vendored parser to own, including a re-sync burden
  documented in `VENDOR.md`. Every local change is marked with a `thlibo:`
  comment specifically so a future re-sync can find it.
- Two processors now claim `^%PDF-`. That is a routing question, and ADR
  0014's tier-1 tiebreak answers it: within rooted format signatures,
  script/native beats prompt and native beats script. `pdf-filter` wins the
  fast path; `pdf-to-md` remains reachable for the OCR rasterization path.
  A regression test pins this, including the reversed alphabetical case so
  it cannot pass by luck.
- Geometry table detection is genuinely harder than pdfplumber's, and we
  now reject more aggressively than it does. On the corpus that is correct
  — every surviving table was inspected and is real, and rejected content
  still appears in the page body — but a real table with unusually tight
  columns would be missed. The threshold is one constant, and the corpus
  evidence for it is recorded above.

**Reversible if needed:**

- `pdf-to-md` is unchanged and still installed. Deleting `pdf-filter`'s
  `init()` registration restores the Python path exactly, which is the
  rollback if the native tier misbehaves in the field.

## References

- gopdf: https://github.com/razvandimescu/gopdf (MIT, zero deps, no CGO) —
  vendored at v0.9.5; see `internal/pdf/VENDOR.md`
- pdfcpu text extraction: https://github.com/pdfcpu/pdfcpu/issues/122
  (open since 2019-10-29)
- Adobe PDF accessibility / tagged PDF:
  https://www.adobe.com/accessibility/pdf.html
- ADR 0007 — the Python decision this supersedes
- ADR 0009 — Gemma-vision OCR, which still owns the scanned-page tier
- ADR 0010 — native Go processors, which held `pdf-to-md` back
- ADR 0014 — fast-path precedence, which resolves the `^%PDF-` collision
