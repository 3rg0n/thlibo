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
- `fuzz_test.go` — upstream ships 102 tests and zero fuzz targets.
- `lexer_test.go`, `bounds_test.go`, `helper_test.go` — regression tests
  for the defects below, and hand-built fixtures replacing upstream's
  `NewCreator()`-based ones (we dropped the generation API).

Correctness fixes:

- `text.go` — BDC handling discarded the inline properties dictionary, so
  every span carried `MCID: -1` and the structure tree could not be bound
  to its text. Added a bounded `parseInlineDict`.
- `reader.go` — `/Differences` narrowed a file-controlled code with
  `byte(v)`, so `[300 /alpha]` aliased onto `0x85` and overwrote whichever
  glyph legitimately sat there. Silent text corruption, not a crash. Now
  out-of-range codes are dropped.

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
correct extraction from a corrupt document has no right answer.
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
