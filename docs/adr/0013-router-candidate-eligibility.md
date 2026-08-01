# 0013. The router only sees processors it can usefully pick

- Status: accepted
- Date: 2026-08-01

## Context

thlibo exists to reduce the tokens a session sends to the frontier model.
Tokens thlibo itself spends therefore have to earn their keep, and the
routing call was not paying for itself.

Measured on the shipped built-in set at v0.11.2, the routing prompt was
**8,747 characters of system prompt plus a 383-character schema — roughly
2,332 tokens — to classify a 200-character sample** (`router.RouteInput`).
That is a ~44:1 ratio of prompt to input.

The prompt was large because it listed all 16 registered processors with
their full `description`, and descriptions are written for humans:
`lint-filter`'s 684 characters document its TSV output columns and the
`LINT_MAX_PER_RULE` env knob; `pdf-to-md`'s 607 explain the ADR 0009 OCR
handoff. None of that helps a router decide whether a given 200-character
sample is lint output. 5,848 characters of it shipped on every routing
call.

Worse, most of those candidates were unreachable by that path anyway:

- **13 of 16** processors declare a `match` regex, so
  `Registry.MatchFastPath` dispatches before the router is ever consulted
  (architectural invariant #4). On input their own regex declined, they
  either return the bytes unchanged (`git-filter`, `trivy-filter`) or
  return nothing at all (`npm-filter`, `cargo-filter` — measured 131 → 0
  bytes, caught by the middleware's empty-output guard). Either way the
  result is a fallback.
- `cordon-filter` is dispatched by name as `ndjson-filter`'s
  over-collapse fallback (`middleware.cordonFallback`), never
  model-selected.
- `shorthand` has its own subcommand and rewrites prose documents *in
  place*. A router picking it for tool output is a correctness bug, not
  merely a wasted call.

Leaving exactly one processor — `compress` — whose selection the model
could meaningfully affect.

## Decision

**Split "describe to a human" from "describe to the router", and offer
the router only the candidates it can usefully pick.**

Two new descriptor fields:

- `route_hint` — the one-line "when should I be picked?" summary sent to
  the model. Falls back to `description` when absent, so a user processor
  written before this field still routes.
- `routable: false` — keeps a processor out of the candidate set. It does
  **not** make the processor unreachable: fast-path match, an explicit
  chain, and hardwired dispatch all still work. Set on `shorthand` and
  `cordon-filter`.

`Descriptor.RouterEligible()` resolves an explicit `routable:` first,
otherwise defaults to "eligible unless a `match` regex already covers
it". `Registry.RoutableNames()` exposes the filtered set, and it feeds
three places that must agree:

1. the candidate list in the system prompt,
2. the `enum` in the `response_format` JSON Schema — if these two diverge
   the grammar permits a name the prompt never offered,
3. `parseRouteResult`'s validation. This third one is the safety-relevant
   one: a backend that ignores `response_format` can emit any name, and
   validating against the *full* registry would let the model select
   `shorthand` for tool output — exactly the correctness bug
   `routable: false` exists to prevent. Rejected names are reported as
   `Unknown` so the caller logs them (THREAT_MODEL.md finding #12).

Finally, `ClientAdapter.Ask` short-circuits to passthrough when
`RoutableNames()` is empty, spending no round-trip at all.

## Consequences

- Routing prompt drops from **~2,332 tokens to ~123** (8,747 + 383 chars
  → 330 + 163 chars), a 95% reduction, measured on the shipped built-in
  set.
- With one candidate the call is now a binary "compress this or not?" —
  123 tokens to gate a potentially multi-thousand-token compression call.
  It earns its keep, so it stays; replacing it with a local heuristic was
  considered and rejected as a worse trade than it looks.
- A registry where every processor is fast-path-covered makes no
  inference call whatsoever.
- The router can no longer select a processor that would corrupt output,
  even on a backend that ignores structured-output constraints.
- Cost: a user processor with both no `match` regex and no `route_hint`
  now ships its full `description` to the model — unchanged from before,
  but now it is the only thing doing so. Documented as the reason to set
  `route_hint`.
- `router.Ask` (a package-level function with zero callers that duplicated
  `askDetailed`) was deleted in the same change.
