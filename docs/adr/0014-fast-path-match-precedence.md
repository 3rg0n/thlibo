# 0014. Fast-path match precedence: format signatures beat line shapes

- Status: accepted
- Date: 2026-08-02

## Context

Architectural invariant #4 says the fast path runs before the router: each
processor's `match` regex is checked, and a hit dispatches immediately with
no inference call. That is a large part of why thlibo is cheap. It also
means `match` is the *only* evidence used to pick a processor, so how ties
are broken is a correctness question, not a tidiness one.

Until now `Registry.MatchFastPath` broke ties by iteration order:

```go
for _, n := range r.order {          // r.order is alphabetical
    if !d.MatchesFastPath(input) { continue }
    if d.Type == KindScript || d.Type == KindNative {
        return d                      // first alphabetical hit wins
    }
    ...                               // prompt processors deferred
}
```

The only ranking was script/native over prompt. There was no notion of how
*strong* a match was, and alphabetical order is not a statement about
anything.

This produced a real, shipped misroute (#97), found during v0.11.3-rc.1
cross-platform e2e and reproduced identically on the v0.11.2 GA — so it is
pre-existing, not a regression:

- `pdf-to-md` matches `^%PDF-` — anchored at byte 0, a definitive
  statement about what the input *is*.
- `go-test-filter` matches
  `(?m)^\s*=== RUN\s|^\s*--- (?:PASS|FAIL|SKIP):|^(?:ok|FAIL|\?)\s+\S+\s`
  — unanchored line shapes, matched anywhere.

On a 6.89 MB paper, `go-test-filter`'s third alternative matched by
coincidence at offset 199970 inside a compressed PDF stream. Because
`"go-test-filter" < "pdf-to-md"`, the coincidence won:

| path | result |
|---|---|
| `thlibo case` (via `MatchFastPath`) | 6893854 → **6892683** bytes, still raw PDF |
| `pdf-to-md/run.py` invoked directly | 6893854 → **87623** bytes of markdown |

A filter that should have delivered 98.7% delivered 0.02%. The failure is
content-dependent — three other PDFs in the same round routed correctly —
which makes it intermittent and hard to attribute in the field.

Investigating the fix surfaced a second, wider case that anchoring alone
does not cover. This repo's own release artifacts —
`thlibo-windows-amd64.zip` and `thlibo-linux-amd64.tar.gz` — *also* false-
match `go-test-filter`, and no signature processor exists to outrank it.
Under rc.1 they were silently line-filtered, losing 342 and 385 bytes out
of a compressed stream. For a binary container that is not wasted work,
it is corruption.

## Decision

Rank fast-path candidates by strength of evidence, and refuse to let
text-oriented filters claim non-text input.

**Three tiers, strongest first:**

1. **Format signatures** — a *rooted* `match` that can only fire at the
   start of the input (`^%PDF-`, `\A...`). This says what the input is, so
   it outranks everything.
2. **Line shapes, script/native** — an unrooted match found anywhere.
   Deterministic and fast, so still preferred over prompt processors
   (ADR 0010: native ranks with script).
3. **Line shapes, prompt** — broad substrings (`(?i)traceback|fatal`),
   the easiest to fire by accident.

Tiebreak within a tier stays stable alphabetical, so dispatch remains
deterministic.

**Rootedness is derived from the pattern, not declared.** `rootedPattern`
parses `match` with `regexp/syntax` and walks the tree for a guaranteed
`OpBeginText` in leading position; `matchRooted` is set in `validate()`
beside the existing `regexp.Compile`. Three properties motivated deriving
rather than adding a `priority:` field:

- It cannot drift out of sync with the regex it describes.
- `match` semantics are a public-ish contract for user processors; a new
  descriptor field would extend that contract, and this does not. A user
  filter with an anchored signature outranks a builtin line shape for
  free.
- The flag sensitivity falls out correctly for free. `(?m)^x` parses to
  `OpBeginLine`, not `OpBeginText`, so every existing multiline builtin is
  classified as a line shape — which is exactly right, and is what makes
  the split derivable at all. Of the 16 shipped builtins, `pdf-to-md` is
  the *only* rooted one, so this tier was already latent in the data.

Alternation requires **every** branch rooted: `^%PDF-|garbage` can match
mid-input, so it is not a signature.

**Binary guard.** `binaryLooking` drops all line-shape candidates (tiers 2
and 3) when the input's first 8 KiB contains a NUL byte — the same test
`internal/casefile.binarySource` already uses, for the same reason. Valid
UTF-8 never contains a NUL; every binary format thlibo handles trips it
immediately. Signatures are deliberately **exempt**: a format signature on
a binary container is precisely the right match.

Both halves are needed. Tier 1 fixes PDF-vs-go-test; only the guard covers
the archives, which have no signature processor to win on their behalf.

## Consequences

- #97 is fixed at the class level, not just for PDF: the real 6.89 MB
  paper now routes to `pdf-to-md` (6893854 → **87623** bytes, 98.73%,
  outline intact — byte-identical to invoking `run.py` directly).
- Release archives now pass through **byte-exact** where rc.1 silently
  stripped bytes from their compressed streams. This closes a latent
  corruption path, not merely a wasted dispatch.
- Ordinary text routing is unchanged. Real `go test` output still hits
  `go-test-filter`; script/native still beats prompt within a tier.
- Both fallback directions are safe. A false "text" verdict only restores
  the previous behaviour, and a false "binary" verdict costs a fast-path
  dispatch that would have been operating on non-text anyway.
- New cost: `MatchFastPath` now scans all candidates instead of returning
  on the first script/native hit, plus one 8 KiB NUL scan. Both are
  negligible next to the dispatch they gate, and the short-circuit at
  `middleware.MinBytesForRouting` (invariant #3) still runs first.
- A user processor wanting signature precedence anchors its regex with `^`
  or `\A` and omits `(?m)`. Nothing to configure.
- Regression coverage in `internal/processors/signature_test.go` pins the
  derivation against the real builtin patterns, so a future filter with a
  loose pattern cannot silently start winning.
