# 0006. Fail open during the inferd bootstrap window

- Status: accepted
- Date: 2026-05-18

## Context

ADR 0005 moves inference to a separate `inferd` daemon. That
daemon has a bootstrap window — first boot may take hours
(5 GB model download on a slow link), subsequent boots take
seconds (mmap of the cached file), and `restarting` events can
occur at any time when the operator triggers a model refresh or
the engine supervisor restarts the backend.

During any of those windows, the inference Unix socket / named
pipe **does not exist on disk**. That's intentional, per the
inferd protocol v1 spec: `infer.sock` only binds when the daemon
is fully ready. The socket's existence is the readiness contract.

Thlibo's compression path needs a behaviour for tool calls that
arrive while the socket is absent. Two shapes were considered:

1. **Fail closed.** Refuse to dispatch the tool call until inferd
   is ready. Block, error, or retry.
2. **Fail open.** Pass the original tool output through unchanged
   and let the AI client see it raw.

Fail-closed has the appeal of "if compression isn't working, we
should know." But thlibo sits in the critical path of every Bash
tool call in Claude Code. Blocking or erroring on a hook breaks
the Bash tool entirely. Users would experience intermittent Claude
Code outages every time they reboot, every time inferd restarts,
every time a model refresh happens — most of those events are
operator-triggered, not user-visible, and the user has no idea
why their AI session suddenly stopped working.

Fail-open is also already the existing fail-closed-contract
precedent in this codebase. Every error path in the middleware
today returns the original bytes:

- Daemon down → original
- Script processor non-zero exit → original
- Eval gate fails on shorthand → original
- Short-circuit under 2000 bytes → original (no work done)

Bootstrap-window passthrough is not a new behaviour; it's an
existing behaviour applied to a new triggering condition.

## Decision

Thlibo implements **passive readiness** as defined in inferd
protocol v1 §6.3: connect-and-retry against the inference socket,
with an immediate passthrough on connect failure.

Pseudocode for the prompt-processor dispatch path:

```
client, err := inferd.DialUnix(ctx, inferdSocketPath, dialOpts)
if err != nil {
    if isTransientConnect(err) {
        // ECONNREFUSED on Unix; ERROR_FILE_NOT_FOUND / pipe-busy on Windows.
        // inferd is not ready — bootstrap, restart, drain.
        // Same fail-open path as ADR 0002's "engine_dead" today.
        return passthroughOriginalBytes(input), nil
    }
    return nil, err  // EACCES / malformed addr / other non-transient
}
defer client.Close()

resp, err := client.Generate(ctx, req)
if err != nil {
    return passthroughOriginalBytes(input), nil
}
return resp.Compressed, nil
```

The dispatch path **does not consult the admin socket**. Admin
socket consumption is reserved for `thlibo doctor` and the
`/caselog` skill, where progress UX during the 5 GB first-boot
download is genuinely useful. Inference dispatch stays simple:
one connect attempt per call, passthrough on failure, no
goroutines, no cached state, no admin-socket subscription.

Script processors (`git-filter`, `npm-filter`, `cargo-filter`,
`pytest-filter`, `ndjson-filter`, `stacktrace-filter`) are
unaffected. They don't use inferd. They keep working through
every bootstrap window.

Connect timeout is 100 ms. Faster than network RTT, slow enough
to traverse a UDS / named pipe under load. Tool-call latency
penalty during a passthrough is bounded at 100 ms; in practice
it's microseconds for the connect-refused case.

Logging on every passthrough is one stderr line:
`thlibo: inferd not ready; passthrough on this call`

Suppressed by `THLIBO_QUIET=1`. Visible by default so users
notice persistent outages (vs. the brief restart blips).

## Consequences

**Easier:**

- The compression path stays simple. One connect, one passthrough
  fallback, no state machine.
- Thlibo never breaks Claude Code. Worst-case during inferd
  bootstrap is "compression isn't applied right now," not "the
  Bash tool is broken."
- Inferd can ship arbitrary lifecycle events (restarts, drains,
  model swaps) without coordinating with thlibo. The contract is
  the socket's existence, nothing more.
- New consumers of inferd (other middleware, future thlibo
  successors) inherit the same passthrough discipline by
  copy-pasting one function.

**Harder:**

- Compression is silently absent during bootstrap windows. A user
  who reboots and immediately starts using Claude Code will have
  their first few minutes of tool calls go through uncompressed.
  Mitigated by the stderr log line and by inferd's autostart
  registering at boot.
- "Why isn't compression working?" becomes a slightly harder
  diagnostic. `thlibo doctor` (which DOES consult the admin
  socket) is the canonical answer to that question — see ADR
  0007 (planned, when doctor lands).
- The 100 ms connect timeout is a chosen value. If inferd ever
  takes longer than 100 ms to accept a connection on a
  steady-state ready socket, every tool call eats that timeout
  before passthrough. Validate during inferd integration; tune
  if needed; keep within v1 if possible.

## References

- ADR 0005: extract inference to inferd
- inferd protocol v1 §6.3 (passive readiness): [`docs/inferd-admin-protocol-v1.md`](../inferd-admin-protocol-v1.md#63-the-passive-readiness-alternative)
- Existing fail-open precedent: `cmd/thlibo/shorthandcmd/shorthand.go:emitOriginal`,
  `internal/middleware/pipeline.go` short-circuit + chain fallback
