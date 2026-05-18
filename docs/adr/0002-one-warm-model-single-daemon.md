# 0002. One warm model, single daemon

- Status: superseded by [0005](0005-extract-inference-to-inferd.md)
- Date: 2026-04-15 (carried from spec v0.1)

## Context

Thlibo's compression path sometimes calls a local Gemma 4 model
via llamafile to summarise unfamiliar tool output (the `compress`
and `casefolder` prompt processors). Model load time for Gemma 4
E4B Q4_K_M is tens of seconds on CPU and has a noticeable memory
footprint. Per-request model loads would be fatal to latency.

Three shapes were considered:

1. **One warm model per daemon lifetime, lazy-loaded at daemon
   start.** Single instance enforced by a filesystem lock.
2. **Short-lived per-invocation engine** spawned by `thlibo exec`.
   No daemon required, simpler ops story, but unacceptable
   latency.
3. **Engine pool** (N warm models). Allows parallelism inside a
   single user's workflow, but requires backpressure signalling,
   per-engine memory, and a much more elaborate lifecycle.

## Decision

One warm model per daemon. Single-instance enforced by
`/run/thlibo/thlibod.lock` (or the platform equivalent). Queue
depth is 1 active + 10 waiting; requests beyond that return
`queue full` immediately without blocking. Engine crash triggers
a supervisor-driven restart up to 3 lifetime attempts; past that,
the daemon reports `engine_dead` on the admin socket and refuses
further requests.

## Consequences

**Easier:**

- Startup latency is paid once, at daemon start or at OS boot if
  autostart is enabled.
- Per-request latency on the compression path is dominated by
  inference time, not setup.
- The admission queue is trivial: one worker, one counter.
- Memory: one model-sized resident footprint, predictable.

**Harder:**

- No within-user parallelism on the prompt-processor path. A
  large concurrent burst (simultaneous diffs across repos)
  serialises. Queue depth 10 handles realistic bursts; past
  that, the spec deliberately degrades to `queue full` rather
  than queue ballooning.
- The single-instance lock is a shared resource; a second user
  on the same machine can't run a second daemon (intentional —
  one engine per machine matches the memory reality).
- Model swapping at runtime is explicitly out of scope. Changing
  GGUFs requires restarting the daemon.

## References

- Spec: `.plan/thlibo-spec.md` §Architectural invariants 6 and 7
- Implementation: `internal/daemon/lifecycle.go`, `internal/queue/`
