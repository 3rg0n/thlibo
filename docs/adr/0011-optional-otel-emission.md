# 0011. Optional OpenTelemetry emission (metrics + events)

- Status: accepted
- Date: 2026-07-14

## Context

thlibo compresses AI-coding-assistant tool output and can save a
material fraction of the tokens a session would otherwise spend. Two
audiences want to *see* that: a local developer curious how much
thlibo saved them, and an organisation that wants to aggregate
"thlibo saved us N tokens/month" across many developers. Today thlibo
emits nothing an external system can consume — `internal/logx` writes
local NDJSON activity records and its own header comment says
"anything more elaborate belongs in an external OTEL exporter."

The obvious shape is OpenTelemetry: emit standard OTLP and let the
operator own the collector, storage, and dashboards. Claude Code
already ships exactly this model (`code.claude.com/docs/en/
monitoring-usage`) — off by default, enabled by a master flag,
configured through the standard `OTEL_*` environment variables,
pointed at the operator's own collector. Mirroring that model gives
thlibo users a surface they already understand.

Two properties of thlibo make a naive OTel integration wrong:

1. **The emitters are short-lived.** The subcommands that do
   compression — `rewrite`, `exec`, `compress`, and the batch `case`
   command — are spawned per tool call (or per file) and exit in well
   under a second. The OTel SDK's default `PeriodicReader` flushes
   every 60 s and its `BatchSpanProcessor`/log processor batch on a
   timer; a process that exits in 200 ms would drop everything it
   recorded. This is the opposite of Claude Code's single long-lived
   session process, and it dictates the flush strategy.

2. **thlibo sits in the critical path of every matched tool call.**
   The cardinal rule (ADR 0006) is that thlibo must never break the
   AI client. An exporter that blocks on an unreachable or slow
   collector would add latency to — or hang — every Bash tool call.

thlibo also processes sensitive bytes: source code, diffs, logs,
file paths, shell commands. The v0.1 threat model's public claim is
"nothing leaves localhost." Any egress path has to be named and
bounded so that turning telemetry on cannot leak content.

## Decision

thlibo emits **OpenTelemetry metrics and events (logs)**, **off by
default**, enabled by a master flag, configured entirely through the
standard `OTEL_*` environment variables, and drained to the operator's
own collector. thlibo owns emission only; the operator owns the
endpoint, transport, storage, and dashboards. No built-in stats view,
no local metric store, no bundled backend.

**Enable + configure (Claude Code parity).**

| Variable | Meaning | Default |
|---|---|---|
| `THLIBO_ENABLE_TELEMETRY` | master enable | unset → **off** |
| `OTEL_METRICS_EXPORTER` | `otlp` \| `console` \| `none` | `otlp` |
| `OTEL_LOGS_EXPORTER` | `otlp` \| `console` \| `none` | `otlp` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | collector endpoint | `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` \| `grpc` | `http/protobuf` |
| `OTEL_EXPORTER_OTLP_HEADERS` | auth headers | — |
| `OTEL_SERVICE_NAME` / `OTEL_RESOURCE_ATTRIBUTES` | resource / org labels | `thlibo` / — |

When `THLIBO_ENABLE_TELEMETRY` is unset the telemetry package returns
a **no-op recorder**: no SDK is constructed, no goroutines start, no
allocations happen on the hot path. Disabled is the zero-cost default,
not a configured-but-silent exporter.

thlibo reads these from the **process environment**, i.e. the user's
shell/profile — not from the AI client's in-process config. Claude
Code deliberately does not pass its own `OTEL_*` to hook subprocesses,
so thlibo's telemetry is configured independently of the client's. A
`[telemetry]` block in `~/.thlibo/config.yaml` is a fallback for users
who cannot set environment variables; the environment wins on
conflict.

**Signals.** Metrics (the durable aggregate) plus per-invocation
events, the latter carried on the OTel **logs** signal (via
`OTEL_LOGS_EXPORTER`) — in OTel terms an "event" is a log record with
an `event.name`. Traces are out of scope for this ADR — the
savings/usage story is fully served by metrics + events, and
per-invocation spans add the most hot-path weight for the least
reporting value. A future ADR may add them behind their own beta flag
if a latency-debugging need appears.

Metrics, namespaced `thlibo.*`:

| Metric | Type | Unit | Key attributes |
|---|---|---|---|
| `thlibo.invocations` | Counter | `{invocation}` | `path`, `outcome`, `tool` |
| `thlibo.bytes.processed` | Counter | `By` | `direction` (input/output), `processor` |
| `thlibo.bytes.saved` | Counter | `By` | `processor`, `kind` |
| `thlibo.compression.ratio` | Histogram | `1` | `processor` |
| `thlibo.dispatch.duration` | Histogram | `s` | `processor`, `kind` |
| `thlibo.fallbacks` | Counter | `{fallback}` | `reason` |

Every attribute above is a **fixed, low-cardinality enum or a
size/duration** — never free-form bytes. Their closed value sets:

- `path` — the middleware **decision path**, *not* a filesystem path:
  one of `short_circuit`, `fast_path`, `router`, `passthrough`.
- `outcome` — `compressed`, `passthrough`, or `fallback`.
- `kind` — the processor **type**: `native` (in-process Go filter),
  `script` (subprocess entry), or `prompt` (inferd inference).
- `direction` — `input` or `output`.
- `reason` — the fallback trigger enum: `script_error`,
  `empty_output`, `router_error`, `inferd_unreachable`, `timeout`.
- `tool` — the AI-client tool name (`Bash`, `Read`, …), a closed set.
- `processor` — the processor name; **built-in** names verbatim
  (closed source-reviewed set), **user** names redacted to `"custom"`
  (see "No content, ever" below).

Savings are reported in **exact bytes** (`thlibo.bytes.saved` =
`bytes_in − bytes_out`), not tokens. thlibo has no tokenizer and the
destination model's tokenizer is unknown; the consumer converts bytes
→ tokens → cost downstream in their own dashboard. This keeps thlibo's
numbers ground-truth and free of a per-invocation tokenizer
dependency.

One event, `thlibo.compression`, emitted per invocation carrying only
the same fixed fields: `{ processor, path, outcome, bytes_in,
bytes_out, duration_ms }` — where `path` / `outcome` / `processor` are
the enums defined above (`path` is the decision path, not a file
path).

**Short-lived-process flush.** Because the emitting subcommands exit
in under a second, thlibo does **not** rely on the periodic flush.
The telemetry lifecycle is: initialise once at process start (only on
the emitting subcommands), record synchronously in-process during the
run, then **force-flush and shut down on exit** with a fixed 2-second
bounded timeout (see Consequences for the rationale on that bound).
Delta temporality is used for metrics so each short-lived process
emits a self-contained, non-cumulative datapoint.

**Fail open (ADR 0006 extended to egress).** Every telemetry
operation is best-effort. SDK construction failure, an unreachable or
slow collector, a flush timeout — none of these change the tool
output or delay it beyond the bounded flush window, and none produce a
non-zero exit. Telemetry that cannot be delivered is dropped
silently. Telemetry never breaks the AI client. The recommended
deployment is therefore a **collector on localhost** (`:4318`), so the
on-exit flush is a fast local hop and the collector forwards upstream
on its own schedule; a direct remote endpoint works but spends its
latency budget on the hook's critical path.

**No content, ever.** In this ADR's scope thlibo emits only sizes,
counts, durations, and a fixed set of enum labels. It never emits tool
output, prompt or completion text, shell commands, file paths, or any
other free-form bytes — there is no `OTEL_LOG_*` content-capture
opt-in of the kind Claude Code offers. The one variable-cardinality
label is the processor name: **built-in** processor names
(`git-filter`, `compress`, …) are emitted verbatim because they are a
closed, source-reviewed set; **user** processor names are redacted to
the constant `"custom"` (matching Claude Code's treatment of
user-authored skill/command names), because a user processor name is
attacker-influenceable and potentially sensitive.

## Consequences

**Easier:**

- Operators get a standard OTLP feed they already know how to
  consume. Point it at any collector/Grafana/Honeycomb; thlibo does
  not care.
- The savings story is answerable from `thlibo.bytes.saved` without
  thlibo shipping a store, a UI, or a tokenizer.
- Disabled-by-default with a no-op recorder means zero cost and zero
  behaviour change for every user who does not opt in — the common
  case.
- The fail-open discipline is the same one every other error path in
  thlibo already follows; the exporter is just one more thing that can
  fail without consequence.

**Harder:**

- A short-lived process that flushes on exit adds a small, bounded
  latency to the emitting subcommands **when telemetry is enabled** —
  the flush timeout, worst case, and near-zero against a localhost
  collector. The bound must be kept tight (single-digit seconds, and
  ideally far less) or a down collector would be felt on every tool
  call. Validate under a dead-endpoint test.
- Delivery is best-effort: with force-flush-on-exit against a busy or
  briefly-unreachable collector, some datapoints can be dropped. This
  is acceptable for usage/savings aggregation (trend data, not
  billing) and is the deliberate trade for never blocking the client.
- This is a genuine posture change from the v0.1 "nothing leaves
  localhost" claim. It is opt-in and content-free, but it is egress,
  and it must be named in `THREAT_MODEL.md` (addendum dated
  2026-07-14) and the README so users are not surprised.
- thlibo now depends on the OpenTelemetry Go SDK and OTLP exporters.
  Module versions are verified at build time (not taken from
  training-data assumptions) and tracked by Dependabot like every
  other dependency. **Maturity caveat:** the metrics and traces SDK
  modules are stable (`go.opentelemetry.io/otel*` v1.x), but the
  **logs** SDK + OTLP-logs exporter — which carry the per-invocation
  `thlibo.compression` event — are still pre-1.0 (v0.x) as of this
  writing and may have breaking API changes. The event signal is the
  less load-bearing of the two (metrics carry the durable savings
  aggregate), so a churny logs API is tolerable; if it proves
  unstable, events can be dropped without losing the core story.
- The on-exit flush timeout is **fixed at 2 seconds**, not operator-
  configurable. It is a latency ceiling on the hook, not a delivery
  guarantee: against the recommended localhost collector the flush
  completes in microseconds; a misconfigured/unreachable **remote**
  endpoint makes every emitting invocation wait up to that 2 s before
  dropping the batch and returning the (unchanged) tool output. This
  is the deliberate cap that keeps a dead collector from hanging the
  client. Validated by a dead-endpoint test (the flush returns within
  the bound and the tool output is byte-identical).

## References

- ADR 0006: fail open during the inferd bootstrap window (the
  never-break-the-client rule this ADR extends to the egress path)
- `THREAT_MODEL.md` — 2026-07-14 addendum naming the opt-in telemetry
  egress path (supersedes the MA-4/T19 "nothing leaves localhost"
  claim for the telemetry-enabled configuration)
- Claude Code monitoring model this mirrors:
  `https://code.claude.com/docs/en/monitoring-usage`
- OpenTelemetry environment-variable spec (`OTEL_*`):
  `https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/`
- `internal/logx/logx.go` header comment anticipating this
  ("anything more elaborate belongs in an external OTEL exporter")
