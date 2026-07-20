// Package telemetry is thlibo's optional OpenTelemetry emission layer
// (ADR 0011). It is OFF by default: unless THLIBO_ENABLE_TELEMETRY is
// set, Init returns a no-op Recorder that constructs no SDK, starts no
// goroutines, and allocates nothing on the hot path.
//
// When enabled, thlibo emits metrics (the durable savings aggregate)
// and one per-invocation event, configured entirely through the
// standard OTEL_* environment variables and drained to an
// operator-owned collector. thlibo emits only — it owns no store, no
// dashboard, and no endpoint. See ADR 0011 and the 2026-07-14
// THREAT_MODEL addendum.
//
// Two invariants are load-bearing:
//
//   - Fail open (ADR 0006 extended to egress): every telemetry
//     operation is best-effort. A missing SDK, an unreachable or slow
//     collector, or a flush timeout drops data silently and never
//     changes the tool output or its exit code.
//   - No content, ever: only sizes, counts, durations, and a fixed set
//     of low-cardinality enum labels are emitted. Never tool output,
//     prompts, commands, or file paths. The one variable label —
//     processor name — is emitted verbatim only for built-ins; user
//     processor names redact to "custom" (see ProcessorLabel).
package telemetry

import (
	"context"
	"os"
	"strings"
	"time"
)

// FlushTimeout bounds the on-exit force-flush + shutdown. It is a
// latency ceiling on the emitting subcommand, not a delivery
// guarantee: against the recommended localhost collector the flush is
// microseconds; an unreachable remote endpoint waits up to this bound
// before dropping the batch (ADR 0011).
const FlushTimeout = 2 * time.Second

// MetricNamespace prefixes every thlibo metric name.
const MetricNamespace = "thlibo."

// EventName is the OTel event (log record) emitted once per invocation.
const EventName = "thlibo.compression"

// Decision-path enum values (the `path` attribute — the middleware
// decision path, NOT a filesystem path).
const (
	PathShortCircuit = "short_circuit"
	PathFastPath     = "fast_path"
	PathRouter       = "router"
	PathPassthrough  = "passthrough"
)

// Outcome enum values (the `outcome` attribute).
const (
	OutcomeCompressed  = "compressed"
	OutcomePassthrough = "passthrough"
	OutcomeFallback    = "fallback"
)

// Fallback-reason enum values (the `reason` attribute on
// thlibo.fallbacks).
const (
	ReasonScriptError       = "script_error"
	ReasonEmptyOutput       = "empty_output"
	ReasonRouterError       = "router_error"
	ReasonInferdUnreachable = "inferd_unreachable"
	ReasonTimeout           = "timeout"
)

// UserProcessorLabel is the constant substituted for any user-authored
// processor name, so a potentially-sensitive name never leaves the
// host (ADR 0011). Built-in names are a closed, source-reviewed set
// and emit verbatim.
const UserProcessorLabel = "custom"

// Invocation is the full, content-free record of one middleware run.
// The middleware builds one of these and hands it to the Recorder
// exactly once per Process call; the Recorder fans it out to the
// individual metrics and the event. Every string field is a fixed
// enum (see the Path*/Outcome*/Reason* constants) or an
// already-redacted processor label — never free-form bytes.
type Invocation struct {
	Tool      string        // AI-client tool name (Bash, Read, …); "" → "unknown"
	Path      string        // decision path enum (Path*)
	Outcome   string        // outcome enum (Outcome*)
	Processor string        // redacted processor label, or "" when none ran
	Kind      string        // processor type: native|script|prompt, or ""
	BytesIn   int           // input size
	BytesOut  int           // output size (== BytesIn on passthrough)
	Duration  time.Duration // whole-decide duration
	Fallback  string        // fallback reason enum (Reason*), or "" if none
}

// Recorder receives content-free telemetry. Every method is safe to
// call on a nil-behaving no-op and must never panic or block beyond
// the flush bound.
type Recorder interface {
	// RecordInvocation emits the metrics and the event for one run.
	// Best-effort; never errors out of the caller.
	RecordInvocation(inv Invocation)
	// Shutdown force-flushes and releases the SDK. Called once on
	// process exit, bounded by FlushTimeout. No-op recorders return nil.
	Shutdown(ctx context.Context) error
}

// ProcessorLabel returns the attribute value for a processor name:
// the name verbatim for built-ins, or the constant UserProcessorLabel
// for user-authored processors (whose names are attacker-influenceable
// and potentially sensitive). An empty name passes through as "".
func ProcessorLabel(name string, builtin bool) string {
	if name == "" {
		return ""
	}
	if builtin {
		return name
	}
	return UserProcessorLabel
}

// Enabled reports whether the master flag THLIBO_ENABLE_TELEMETRY is
// set to a truthy value. Mirrors logx's THLIBO_LOG truthiness set.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("THLIBO_ENABLE_TELEMETRY"))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

// noop is the disabled Recorder: it does nothing and allocates
// nothing. Init returns this whenever telemetry is off or the SDK
// fails to construct (fail open).
type noop struct{}

func (noop) RecordInvocation(Invocation)      {}
func (noop) Shutdown(context.Context) error   { return nil }

// NoopRecorder returns the shared disabled Recorder. Exposed for tests
// and for callers that want an explicit no-op.
func NoopRecorder() Recorder { return noop{} }
