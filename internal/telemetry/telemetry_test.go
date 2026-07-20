package telemetry

import (
	"context"
	"testing"
	"time"
)

// TestDisabledReturnsNoop: with the master flag unset, Init returns the
// no-op recorder — no SDK, no providers. This is the zero-cost default.
func TestDisabledReturnsNoop(t *testing.T) {
	t.Setenv("THLIBO_ENABLE_TELEMETRY", "")
	rec := Init(context.Background())
	if _, ok := rec.(noop); !ok {
		t.Fatalf("disabled telemetry must return noop recorder, got %T", rec)
	}
	// Must be safe to use and shut down.
	rec.RecordInvocation(Invocation{Path: PathFastPath, Outcome: OutcomeCompressed, BytesIn: 100, BytesOut: 10})
	if err := rec.Shutdown(context.Background()); err != nil {
		t.Fatalf("noop Shutdown returned error: %v", err)
	}
}

// TestEnabledWithNoneExportersFallsOpen: master flag on but both
// signals set to "none" → nothing to emit → no-op (fail open, not a
// half-built recorder).
func TestEnabledWithNoneExportersFallsOpen(t *testing.T) {
	t.Setenv("THLIBO_ENABLE_TELEMETRY", "1")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	rec := Init(context.Background())
	if _, ok := rec.(noop); !ok {
		t.Fatalf("both exporters none must yield noop, got %T", rec)
	}
}

// TestEnabledConsoleBuildsRecorder: console exporters need no network
// and must construct a live recorder that records + shuts down cleanly
// within the flush bound.
func TestEnabledConsoleBuildsRecorder(t *testing.T) {
	t.Setenv("THLIBO_ENABLE_TELEMETRY", "1")
	t.Setenv("OTEL_METRICS_EXPORTER", "console")
	t.Setenv("OTEL_LOGS_EXPORTER", "console")
	rec := Init(context.Background())
	if _, ok := rec.(noop); ok {
		t.Fatal("console exporters must build a live recorder, got noop")
	}
	or, ok := rec.(*otelRecorder)
	if !ok {
		t.Fatalf("expected *otelRecorder, got %T", rec)
	}
	if or.invocations == nil {
		t.Error("metric instruments not initialised")
	}
	if or.logger == nil {
		t.Error("event logger not initialised")
	}

	rec.RecordInvocation(Invocation{
		Tool: "Bash", Path: PathFastPath, Outcome: OutcomeCompressed,
		Processor: "git-filter", Kind: "native",
		BytesIn: 5000, BytesOut: 200, Duration: 3 * time.Millisecond,
	})

	start := time.Now()
	if err := rec.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	if d := time.Since(start); d > FlushTimeout+time.Second {
		t.Errorf("Shutdown exceeded flush bound: %v", d)
	}
}

// TestDeadEndpointFailsOpenBounded: enabled, pointed at an unreachable
// OTLP endpoint. Recording must not block and Shutdown must return
// within the flush bound rather than hang — never breaking the client.
func TestDeadEndpointFailsOpenBounded(t *testing.T) {
	t.Setenv("THLIBO_ENABLE_TELEMETRY", "1")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	// 127.0.0.1:1 — nothing listens; connect fails fast/times out.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")

	rec := Init(context.Background())
	// Recording is synchronous but must never block the caller.
	done := make(chan struct{})
	go func() {
		rec.RecordInvocation(Invocation{
			Tool: "Bash", Path: PathRouter, Outcome: OutcomeCompressed,
			Processor: "compress", Kind: "prompt",
			BytesIn: 8000, BytesOut: 500, Duration: time.Millisecond,
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(FlushTimeout + 2*time.Second):
		t.Fatal("RecordInvocation blocked on a dead endpoint")
	}

	start := time.Now()
	// Shutdown against a dead endpoint may return an error — that's
	// fine (fail open); it must just come back within the bound.
	_ = rec.Shutdown(context.Background())
	if d := time.Since(start); d > FlushTimeout+2*time.Second {
		t.Fatalf("Shutdown hung on dead endpoint: %v (bound %v)", d, FlushTimeout)
	}
}

func TestEnabled(t *testing.T) {
	cases := map[string]bool{
		"1": true, "true": true, "on": true, "yes": true, "TRUE": true, " 1 ": true,
		"": false, "0": false, "false": false, "off": false, "no": false, "banana": false,
	}
	for v, want := range cases {
		t.Setenv("THLIBO_ENABLE_TELEMETRY", v)
		if got := Enabled(); got != want {
			t.Errorf("Enabled(%q)=%v want %v", v, got, want)
		}
	}
}

func TestProcessorLabelRedactsUserNames(t *testing.T) {
	if got := ProcessorLabel("git-filter", true); got != "git-filter" {
		t.Errorf("builtin name must pass through, got %q", got)
	}
	if got := ProcessorLabel("my-secret-proc", false); got != UserProcessorLabel {
		t.Errorf("user name must redact to %q, got %q", UserProcessorLabel, got)
	}
	if got := ProcessorLabel("", true); got != "" {
		t.Errorf("empty name must stay empty, got %q", got)
	}
}
