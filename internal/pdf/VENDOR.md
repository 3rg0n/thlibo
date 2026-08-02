# Vendored: gopdf extraction subset

Source: https://github.com/razvandimescu/gopdf @ v0.9.5 (MIT, zero
dependencies, no CGO). Upstream `LICENSE` is preserved verbatim as
`LICENSE.gopdf`.

## Why vendored rather than a module dependency

This code parses untrusted input — a PDF arriving as AI-assistant tool
output is attacker-influenced in the general case. Vendoring buys three
things a `require` line does not:

1. **Local patching.** Upstream ignores `/Encrypt` entirely, which we
   must refuse rather than silently mis-parse (see `security.go`). That
   fix cannot wait on upstream.
2. **A frozen review surface.** 12 stars, single maintainer, v0.9.x. We
   read what we ship and we fuzz it ourselves.
3. **Subsetting.** We keep only what extraction needs.

## What was taken, and what was left

Kept: `objects.go`, `lexer.go`, `parser.go`, `reader.go`, `text.go`,
`stdfonts.go`, `glyphlist.go`, `table.go` — plus the upstream tests, which
are the parity safety net.

Dropped: `creator.go`, `writer.go`, `merge.go`, `edit.go`, `image.go`
(~1,894 lines of PDF *generation* and editing). thlibo only ever reads.
`document.go` was kept but rewritten: upstream's `OpenFile(path)` is gone,
because every caller here arrives with bytes already (a processor reads
stdin, never a path) and a path-taking constructor is only a file-inclusion
sink for an argument no production code supplies.

`filterNames` lived in the dropped `merge.go`; it is reproduced verbatim in
`filters.go` because `readStreamData` needs it.

## Local modifications

Every change is marked with a `thlibo:` comment so a future re-sync can
find it.

New files:

- `security.go` — `/Encrypt` detection. Upstream has no encryption
  support at all (`grep Encrypt reader.go` → nothing), so an encrypted
  PDF parses to garbage instead of erroring. We refuse it explicitly.
- `filters.go` — `filterNames`, carried over from `merge.go`, plus
  real decoders for the image-XObject filter chain. Upstream's
  `applyFilter` returns unknown filters *undecoded* (`default:` branch),
  which silently yields wrong bytes rather than an error.
- `structure.go` — the `/StructTreeRoot` walker (tagged-PDF tier). No
  upstream equivalent; upstream reads tags only as far as `table.go`
  needs them.
- `meta.go` — `/Info` and `/Outline` accessors, with the same bounded-walk
  treatment as the rest.
- `images.go` — `Page.Images()` / `ExtractPageImages`: a content-stream walker
  returning each painted image with its CTM. Upstream has no image accessor at
  all, and `meta.go`'s `HasImages` only ever answered yes/no. The distinction
  matters because `/Resources/XObject` lists what a page *may* paint, not what
  it does or at what size — a corner logo and a full-page scan are
  indistinguishable there, and only the second should reach OCR. Deliberately
  a separate walker from `extractTextWithResources`: that loop is
  corpus-validated byte-identical and is the hot path, so an image concern
  threaded through it would cost allocation and stream resolution on every
  document for no gain. `Page.ScanImage()` is the OCR decision itself.
- `samples.go` — raw-sample decoding, the path `DecodeImageStream` previously
  refused outright. After the byte-level filter runs, a `FlateDecode` image is
  a packed sample array, and turning it into pixels needs the component count,
  bit depth and `/Decode` mapping read explicitly. Supports DeviceGray/RGB/CMYK,
  Indexed, CalGray, CalRGB, Lab (as alternate), ICCBased (via `/N`) and stencil
  masks at 1/2/4/8/16 bpc; refuses Separation and DeviceN, which need a
  PostScript tint-transform evaluator and are not what a scanner emits.
  `DecodeImageStream` is now a thin wrapper over `decodeImage`, and reads the
  colour space straight from the dict — correct only for a direct device name.
  Anything else (an indirect reference, an ICCBased array, a `/CS0`-style
  resource key) needs the `Reader` and page resources to resolve, so callers
  that have them should go through `ImageRef.Decode`.
- `fuzz_test.go` — upstream ships 102 tests and zero fuzz targets.
- `lexer_test.go`, `bounds_test.go`, `filters_test.go`, `helper_test.go`,
  `images_test.go`, `samples_test.go` — regression tests for the defects below,
  and hand-built fixtures replacing upstream's `NewCreator()`-based ones (we
  dropped the generation API).

Correctness fixes:

- `text.go` — BDC handling discarded the inline properties dictionary, so
  every span carried `MCID: -1` and the structure tree could not be bound
  to its text. Added a bounded `parseInlineDict`.
- `reader.go` — `/Differences` narrowed a file-controlled code with
  `byte(v)`, so `[300 /alpha]` aliased onto `0x85` and overwrote whichever
  glyph legitimately sat there. Silent text corruption, not a crash. Now
  out-of-range codes are dropped.
- `filters.go` — two bugs in our *own* `decodeCCITT`, both found by review
  rather than by the corpus, because nothing called `DecodeImageStream` yet.
  It used `ccitt.NewReader` to fill an `image.Gray`'s `Pix`, but that reader
  yields *packed 1-bpp* rows — so it demanded exactly 8× the bytes available
  and every CCITT scan failed with `unexpected EOF` (measured on a real
  2480×3507 fax: 1,087,170 bytes delivered against 8,697,360 wanted). Now
  uses `ccitt.DecodeIntoGray`, which expands bits to bytes itself. Second,
  `Invert` was set to `!blackIs1`; `x/image/ccitt` already emits CCITT's
  default polarity (0 bit = white) as white pixels, so the negation turned a
  text page into a 94.8%-black negative. `Invert` now tracks `/BlackIs1`
  directly — same scan, 5.21% ink.

Termination fixes — all three found by our own fuzz targets, all three
**hangs rather than crashes**, which is the one failure mode thlibo's
fail-open contract cannot absorb (a panic is recovered and the original
bytes pass through; nothing recovers a blocked hook):

- `lexer.go` — `NextToken` dispatches `)`, `{`, `}` to `readKeyword`, whose
  scan loop stopped on a delimiter *without advancing `pos`*. Every caller
  loops until EOF, so one stray `)` in a content stream meant text
  extraction never returned. Found on the one-byte input `")"`.
- `reader.go` — an xref subsection's declared entry count was trusted, and
  `NextToken` returns `(TEOF, nil)` rather than an error, so a header of
  `0 60000000000` spun sixty billion no-op iterations.
- `reader.go` — `/Prev` is a file-controlled offset followed by recursion,
  with no visited set; and `collectPages` walked `/Kids` with neither a
  depth bound nor a cycle guard, so a `/Pages` node naming itself recursed
  until the stack gave out.

File-declared *positions* that reach the lexer — the same shape as the sizes
below, one step earlier. Both found by `FuzzOpenBytes` after `Page.Images()`
widened the target:

- `reader.go` — an xref entry's offset went straight into `Lexer.SetPos`, so a
  subsection line of `-1 0 n` indexed `data[-1]`. The compressed-object path
  (`resolveFromObjStm`) already range-checked its own offset; the uncompressed
  path (`parseObjectAt`) did not.
- `lexer.go` — `SetPos` now clamps. That is not belt-and-braces for the above:
  `readXRefAt` takes its position from `startxref` and from every `/Prev` in
  the update chain, neither range-checked at the call site, and `/Prev -1` is
  a perfectly parseable PDF integer that panicked the same way. Two
  independent paths, one clamp.

File-declared sizes that reach a slice — all in `reader.go`, all found by
review of PR #103 rather than by the fuzzer, and all the same shape: a number
the document asserts about itself, used to index or allocate:

- `/Length` on a stream was sliced directly, so `/Length 999999` in a
  768-byte file panicked with `slice bounds out of range`. A bad length is
  now treated as a missing one — scan for `endstream` — rather than clamped
  to EOF, which would feed the decoders every trailing byte in the file.
- `/N` on a compressed object stream sized `make([]objEntry, n)`, so
  `/N 1000000000` is a ~16 GB ask (`makeslice: len out of range`). Capped at
  `maxObjStmEntries`, and additionally by the stream's own length: two tokens
  per entry means a stream cannot honestly hold more entries than it has
  bytes.
- `/First` plus a per-entry offset indexed the object-stream data unchecked,
  in two places. The second is the cache-all loop, which touches *every*
  entry rather than just the requested one, so it is the more reachable.
- `/Columns`, `/Colors` and `/BitsPerComponent` multiply into the predictor
  row-size allocation. Each is now range-checked *individually*, because the
  product overflows int64 at large values and comes back small and
  innocent-looking — checking only the product is not a check.

These four are panics rather than hangs, so `RunNative`'s recover does keep
the fail-open contract intact. They are still worth fixing at the source: a
recovered panic costs the user all compression on that document, and for a
bogus `/Length` the right answer (find `endstream`) recovers real text.

Upstream's lack of guards here is defensible for a library that reads files
its own caller wrote. thlibo reads whatever an AI client was pointed at, so
they are required.

## Fuzzing

Three targets in `fuzz_test.go`, run past 50M execs each before merge:

```
go test ./internal/pdf/ -run xxx -fuzz FuzzOpenBytes -fuzztime 120s
go test ./internal/pdf/ -run xxx -fuzz FuzzParseInlineDict -fuzztime 120s
go test ./internal/pdf/ -run xxx -fuzz FuzzDecodeTextString -fuzztime 120s
```

`FuzzOpenBytes` asserts only "no panic, no hang, no unbounded allocation" —
correct extraction from a corrupt document has no right answer. It drives
`Page.Images()` and decodes what comes back, which reaches materially more
untrusted structure than the text path alone: the walker follows Form XObjects
into file-controlled `/Resources`, and the sample decoder sizes a row stride
from declared geometry. Seeds cover an inline image, an inline image whose
`/L` lands inside its own data, and a self-referential Form. Widening it that
way is what surfaced the two position bugs above, on a target that had
previously run past 135M execs clean.
`FuzzDecodeTextString` is stricter, because a `/Title` becomes an H1: it
asserts the sanitized result never contains a control rune or a double
space. Crashers are committed under `testdata/fuzz/` and replay on every
`go test`; the named tests in `lexer_test.go` and `bounds_test.go` exist
because a replayed hang surfaces as a package timeout that says nothing
about which walk looped.

## Upstream properties worth keeping true on re-sync

`go.mod` declares no requires; the package imports only the standard
library (plus `golang.org/x/image/ccitt` after our `filters.go`). No
`unsafe`, no `os/exec`, no `net`. Verify that still holds before adopting
a new version.
