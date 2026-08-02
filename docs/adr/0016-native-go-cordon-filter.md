# 0016. Native Go cordon-filter — retiring the numpy dep and the last compression-path Python

- Status: accepted
- Date: 2026-08-02
- Supersedes: [0008](0008-numpy-as-processor-dep.md) (the numpy dependency
  and its soft-import pattern, as they applied to `cordon-filter`)

## Context

ADR 0010 ported 9 deterministic filters to native Go and held two back as
documented exceptions: `pdf-to-md` (PDF format deps, ADR 0007) and
`cordon-filter` (numpy for k-NN scoring, ADR 0008). It named the exit
condition explicitly: *"A future pure-Go PDF library with table + raster
parity, or Go numeric k-NN, could retire the last two Python processors;
revisit ADR 0007 / 0008 then."* ADR 0015 did the PDF half. This is the
other one.

Two things changed the calculus since ADR 0008.

**The numpy argument was about Python, not about the math.** ADR 0008
measured a ~100× gap between vectorised numpy and nested Python loops and
correctly took the dep. That comparison does not transfer: the Go
equivalent is a doubly-nested loop over `[]float64` compiled to machine
code, not an interpreted one. The 40-lines-vs-5-lines and 10s-vs-100ms
trade-offs that justified numpy both evaporate when the host language
isn't Python.

**`cordon-filter` was the only remaining reason a compression-path user
needed Python at all.** `pdf-to-md` survives ADR 0015 for page
rasterization, which is a PDF-users-only concern. So long as cordon was a
script, ADR 0010's "Python-free is conditional" caveat applied to the
ordinary log-compression path too — and cordon fires automatically as
`ndjson-filter`'s over-collapse fallback (`middleware.cordonFallback`),
so a user who never opted into anything could still hit a silently
missing numpy and get passthrough where compression was expected. That
is the exact silent-failure footgun ADR 0010 set out to remove.

The one structural obstacle: cordon is the only filter that makes a
network round-trip. `NativeFilter` was `func([]byte) []byte`, and inferd's
embedding surface (NDJSON on its own socket, inferd ADR 0017) had no
client in `internal/inferd` at all — the package implemented only the
length-prefixed generation wire.

## Decision

**Port `cordon-filter` to native Go and drop the numpy dependency.**

Three supporting changes, each narrower than it might have been:

1. **`NativeCtxFilter` (`func(ctx, []byte) []byte`), alongside — not
   replacing — `NativeFilter`.** `RegisterNative` adapts into a single
   ctx-aware registry, so all 12 existing filters and their tests are
   untouched. Keeping the ctx-free signature is deliberate: it states at
   the type level that a filter does not reach the network, which is true
   of every filter but this one. Two separate registries were rejected —
   a name registered in both would become a silent shadow instead of the
   existing build-time duplicate panic.

2. **`inferd.EmbedClient` in `internal/inferd`.** Same package as
   generation because they share the socket dialers and the timeout knob,
   but a separate file and a separate protocol: one NDJSON request line,
   one response line. Same passive-readiness posture as `Client.Post` — a
   connect failure surfaces as `ErrBackendNotReady` so the caller fails
   open (ADR 0006).

3. **An explicit total-time bound (`CORDON_TIMEOUT`, 30s).** Not a
   nicety, a replacement: as a script processor cordon was bounded by
   `Dispatcher.ScriptTimeout` at 30s, and its own 60s embed timeout was
   never actually reachable. `RunNativeCtx` cannot interrupt a running
   filter, so dropping the subprocess without replacing its ceiling would
   have converted a wedged daemon from a bounded fallback into a hung
   PreToolUse hook (ADR 0012: fail open means fail *fast*).

**Parity is pinned by captured fixtures, not by live Python.** 43
signature/level cases were dumped from the running `run.py` before it was
retired and committed as a Go table. This is the only way to pin parity
for this filter — unlike the ADR 0010 ports, cordon's *output* cannot be
golden-tested against Python, because the ranking is a function of a live
model's embeddings. The signature/level layer is deterministic and is
therefore where parity is asserted; the scoring and selection layers are
tested against a deterministic in-process embedder injected through a
narrow `cordonEmbedder` interface.

`run.py` stays on disk as the reference implementation, matching every
other ported filter (`git-filter` et al. kept theirs). It is no longer
executed — `processor.yaml` is `type: native`.

## Consequences

**Easier:**

- **Python is no longer needed anywhere on the compression path.** The
  remaining use is `pdf-to-md`'s rasterization tier, which only PDF users
  reach. ADR 0010's conditional-Python caveat now has exactly one
  condition instead of two.
- No numpy install, no ~25 MB footprint, no soft-import branch, and no
  class of user who silently gets passthrough because a dep is absent.
- No `python3` fork per dispatch — cordon ran on inputs of 30+ lines as an
  automatic fallback, so it paid interpreter startup on a hot path.
- The k-NN math is now covered by unit tests with hand-computable
  expected values, which the numpy one-liner never was.

**Harder / trade-offs:**

- `internal/processors` now depends on `internal/inferd`. Previously the
  native filters were pure transforms. This is a real coupling and the
  reason `NativeCtxFilter` is deliberately awkward to reach for.
- The brute-force O(n²) scoring is ~40 lines of Go against numpy's 5.
  Accepted: it is the textbook form, and n is bounded by
  `CORDON_MAX_WINDOWS`.
- Parity rests on a captured table that cannot be regenerated. A future
  change to signature logic is a behaviour change, not a test fix, and
  the fixture file says so.
- Scoring is not vectorised or SIMD-accelerated. Not measured as a
  problem at cordon's window counts; if it becomes one, the fix is local
  to `cordonKNNScores`.

**Not changed:**

- The output shape, the `routable: false` posture, the over-collapse
  fallback wiring, and the fail-open contract are all identical. Callers
  and the router see no difference.
- ADR 0008's *soft-import pattern* remains valid where it still applies —
  `pdf-to-md`'s pypdf/pdfplumber (ADR 0007). What this ADR supersedes is
  the numpy dependency itself and the claim that cordon needs it.

## References

- ADR 0008: numpy as a processor dep (superseded by this, for cordon)
- ADR 0010: native Go processors — named this as the exit condition
- ADR 0015: native Go PDF extraction (the other half of ADR 0010's
  carve-out)
- ADR 0006: fail open during the inferd bootstrap window
- ADR 0012: every inference call carries a deadline
- inferd ADR 0017: the embedding socket and its NDJSON wire
- Issue #28: cordon-filter scaffold
- Cordon (Apache-2.0), the algorithmic blueprint:
  https://github.com/calebevans/cordon
