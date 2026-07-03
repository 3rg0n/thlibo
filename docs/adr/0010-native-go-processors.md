# 0010. Reimplement deterministic built-in processors as native Go

- Status: accepted
- Date: 2026-07-03

## Context

thlibo ships 14 built-in processors, embedded via `go:embed`
(`processors/`). Three are prompt processors (`.md`, no code); the
other 11 are **script processors** whose `entry` is a Python `run.py`,
dispatched by the middleware as `python3 <path>` (stdin→stdout, non-zero
exit = fallback; `internal/processors/dispatch.go`).

This makes **python3 a hard runtime dependency for 11 of 14 built-ins**,
yet nothing verifies it: neither `thlibo install` nor the one-liner
installers check for Python. On a host without `python3` (common on
fresh Windows, minimal Linux images), those 11 filters exit non-zero
and the middleware silently falls back to passthrough — the user gets
uncompressed output and no signal why. The README lists "Python 3.8+"
as a prerequisite, but that is easy to miss and impossible to enforce
from a piped one-liner.

Of the 11, a survey of `run.py` sources found:

- **9 are deterministic text/JSON munging** — regex, line state
  machines, `json`/stdlib only, pure stdin→stdout, no external packages,
  no network, no filesystem: `git-filter`, `npm-filter`, `cargo-filter`,
  `pytest-filter`, `go-test-filter`, `ndjson-filter`, `stacktrace-filter`,
  `lint-filter`, `trivy-filter`. These are a clean fit for Go.
- **`pdf-to-md`** depends on `pypdf` + `pdfplumber` (+ Pillow) for text/
  table extraction and page rasterization (the OCR path, ADR 0009). No
  Go PDF library offers parity; porting would mean C bindings (mupdf) or
  shelling out to ImageMagick/ghostscript. Its Python choice is already
  accepted in **ADR 0007**.
- **`cordon-filter`** uses `numpy` for k-NN anomaly scoring over inferd
  embeddings. Its numpy dependency is already accepted in **ADR 0008**,
  with a soft-import + passthrough fallback.

The dispatch surface that a replacement must satisfy is small:
`Dispatcher.Run` switches on `Descriptor.Type` (`KindScript` /
`KindPrompt`); the middleware only calls `Run`/`RunChain`. Adding a
processor kind is one new case plus registration.

## Decision

Reimplement the **9 deterministic filters** as **native Go**, run
in-process, and introduce a new processor kind for them:

- A `KindNative` descriptor type. A built-in native processor's
  `processor.yaml` declares `type: native` + `name`; it has no `entry`
  script, needs no `DiskDir`, and is **not** mirrored to disk.
- A registry maps the native `name` → a Go `func([]byte) []byte` (the
  filter). `Dispatcher.Run` gains a `KindNative` case that calls the
  function in-process — no subprocess, no `python3`, no entry-file
  fingerprint (there is no on-disk entry to swap; the code is the
  compiled binary, already integrity-checked as part of the release).
- Each filter preserves the existing **monotonic guarantee** (emit the
  compressed form only when it is a strict UTF-8 byte win; otherwise
  return input verbatim) and the **fail-safe contract** (any panic is
  recovered → return input unchanged; the middleware never breaks).
- The 9 `run.py` files and their `processor.yaml` `entry:` lines are
  removed from the embedded tree; their `match` / `commands` /
  `command_prefixes` routing metadata is preserved on the native
  descriptors so routing is unchanged.

**`pdf-to-md` and `cordon-filter` stay Python**, unchanged, as
documented exceptions (ADR 0007 and 0008 respectively). They remain
`KindScript` processors dispatched via `python3`. Python therefore stays
an **optional** dependency: needed only for PDF conversion and the
cordon anomaly surfacer, not for the common compression filters.

**User-authored processors are unaffected.** `type: script` with a
`.py`/`.sh`/`.exe`/`.bin` `entry` works exactly as before; `KindNative`
is reserved for built-ins (a user `processor.yaml` declaring
`type: native` is rejected at load — there is no user-supplied Go code
to bind).

Behavior parity is locked with **golden-file tests**: each Go filter is
run against representative fixtures and its output compared to the
Python `run.py` output (kept as reference during the port).

## Consequences

Easier:

- The common compression path (git/npm/cargo/test/lint/trivy/ndjson/
  stacktrace output) works with **no python3 installed** — the
  silent-failure footgun is gone for everything thlibo owns except PDF
  and cordon.
- Faster: in-process function call instead of a `python3` fork per
  dispatch; no interpreter startup, no TOCTOU stat, no timeout goroutine
  for these.
- Fewer moving parts on Windows (no `python3` on PATH, no `.py`
  association concerns).
- One language for the shipped filters; changes are type-checked and
  covered by `go test` + the scanner gate.

Harder / trade-offs:

- ~2600 lines of new Go across 9 filters to write and keep at parity;
  the golden-file fixtures are the safety net.
- Two dispatch mechanisms coexist (`KindNative` in-process, `KindScript`
  subprocess). Acceptable: `KindScript` must stay for user processors
  and the two Python exceptions regardless.
- Python is still required for `pdf-to-md` (PDF users) and
  `cordon-filter` — so "Python-free" is **conditional**, not absolute.
  The installer/README must state Python is optional and name exactly
  what needs it, rather than dropping the prerequisite entirely.
- A future pure-Go PDF library with table + raster parity, or Go numeric
  k-NN, could retire the last two Python processors; revisit ADR 0007 /
  0008 then.
