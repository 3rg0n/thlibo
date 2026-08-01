# 0012. Every inference call carries a deadline

- Status: accepted
- Date: 2026-08-01

## Context

ADR 0006 established that thlibo fails *open*: when inference is
unavailable, the middleware passes the original bytes through and the AI
client is never broken. The implementation covered the cases where
unavailability is *observable* — the dial fails, the socket isn't bound,
the stream closes without a terminal frame — and every one of those
returns promptly.

It did not cover the case where the daemon accepts the connection and
then never answers. inferd can be mid model-load, thrashing on a model
swap, or wedged on a bad GGUF, and in each of those it holds the
connection open. `inferd.Client.Post` imposed no deadline, and every
hot-path caller passed `context.Background()`:

- `cmd/thlibo/execcmd/exec.go`
- `cmd/thlibo/compresscmd/compress.go`
- `cmd/thlibo/casecmd/case.go`

So the wait was unbounded. That is not fail-open — it is a hang, and it
happens inside a PreToolUse hook, which means the AI client blocks on it.
The asymmetry is stark: script processors were already bounded at 30s
(`internal/processors/dispatch.go`), so a hanging *script* was handled
and a hanging *daemon* was not.

Hook hosts impose their own budgets — Cursor's Read hook allows 20s
(`THLIBO_READ_TIMEOUT`), Copilot's allow 30s
(`copilot.HookTimeoutSec`) — so the host eventually kills the hook. But
losing that race means the host's fallback fires instead of thlibo's,
with a worse error message and no telemetry.

## Decision

**Every inference round-trip is bounded, and the bound lives in
`inferd.Client.Post` rather than at the call sites.**

- `DefaultTimeout = 15s`, chosen to sit under the tightest hook budget we
  ship (Cursor's 20s) so thlibo gives up and passes the original bytes
  through *before* the host intervenes.
- Overridable via `$THLIBO_INFERD_TIMEOUT` (Go duration or bare seconds).
  `0` disables the deadline for an operator debugging a slow local model.
  A malformed value falls back to the default rather than erroring — a
  typo in an env var must not break the middleware.
- `Client.Timeout` overrides the environment. Negative means unbounded.
- The deadline is applied with `context.WithTimeout`, which only ever
  tightens, so a caller that already set a shorter deadline keeps it.

Two consequences worth stating explicitly:

**The bound is per round-trip, not per subcommand.** `thlibo case` on a
scanned PDF makes one inferd vision call per page (ADR 0009) and
legitimately runs far longer than 15s in total. Bounding the subcommand
would have broken that feature.

**Expiry is reported as the context error, not the wire symptom.**
Cancellation works by closing the connection (ADR 0007), so an in-flight
read or write fails with "closed pipe" rather than the real cause. `Post`
normalises this: if the context expired, the context error is returned.
Without that, `middleware.dispatchFailReason` classifies a blown deadline
as `script_error` and a wedged daemon is invisible in the metrics —
observability that only works when nothing is wrong is not
observability.

## Consequences

- A wedged daemon now costs 15s of latency once, then passes through,
  instead of hanging the AI client indefinitely.
- The failure is correctly labelled `timeout` in telemetry, so a wedged
  daemon is distinguishable from a broken processor.
- No call site can forget the deadline, because none of them sets it.
- A genuinely slow first call — cold model load on a large GGUF — may now
  time out where it previously waited. This is the intended trade: the
  middleware's job is to not break the client, and inferd's readiness is
  inferd's problem. Operators who want the old behaviour set
  `THLIBO_INFERD_TIMEOUT=0`.
- Regression coverage in `internal/inferd/timeout_test.go` pins the
  wedged-daemon case with a server that reads and never replies.
