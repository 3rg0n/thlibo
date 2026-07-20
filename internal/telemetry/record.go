package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// RecordInvocation emits the metrics and the per-invocation event for
// one middleware run. Best-effort: any nil sub-provider (signal
// disabled or failed to build) is skipped. Never blocks, never errors
// out of the caller.
func (r *otelRecorder) RecordInvocation(inv Invocation) {
	ctx := context.Background()

	if r.invocations != nil {
		r.recordMetrics(ctx, inv)
	}
	if r.logger != nil {
		r.recordEvent(ctx, inv)
	}
}

func (r *otelRecorder) recordMetrics(ctx context.Context, inv Invocation) {
	tool := inv.Tool
	if tool == "" {
		tool = "unknown"
	}

	// thlibo.invocations{path,outcome,tool}
	r.invocations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("path", inv.Path),
		attribute.String("outcome", inv.Outcome),
		attribute.String("tool", tool),
	))

	// thlibo.dispatch.duration{processor,kind} — always recorded (the
	// whole decide() cost, even on passthrough where no processor ran).
	procAttrs := []attribute.KeyValue{}
	if inv.Processor != "" {
		procAttrs = append(procAttrs, attribute.String("processor", inv.Processor))
	}
	if inv.Kind != "" {
		procAttrs = append(procAttrs, attribute.String("kind", inv.Kind))
	}
	r.dispatchDur.Record(ctx, inv.Duration.Seconds(), metric.WithAttributes(procAttrs...))

	// thlibo.bytes.processed{direction,processor} — both directions.
	inAttrs := append([]attribute.KeyValue{attribute.String("direction", "input")}, procAttrs...)
	outAttrs := append([]attribute.KeyValue{attribute.String("direction", "output")}, procAttrs...)
	r.bytesProc.Add(ctx, int64(inv.BytesIn), metric.WithAttributes(inAttrs...))
	r.bytesProc.Add(ctx, int64(inv.BytesOut), metric.WithAttributes(outAttrs...))

	// thlibo.bytes.saved{processor,kind} + compression.ratio{processor}
	// — only meaningful when a processor actually shrank the output.
	if inv.Outcome == OutcomeCompressed && inv.BytesIn > 0 {
		saved := inv.BytesIn - inv.BytesOut
		if saved < 0 {
			saved = 0
		}
		r.bytesSaved.Add(ctx, int64(saved), metric.WithAttributes(procAttrs...))

		ratioAttrs := []attribute.KeyValue{}
		if inv.Processor != "" {
			ratioAttrs = append(ratioAttrs, attribute.String("processor", inv.Processor))
		}
		r.ratio.Record(ctx, float64(inv.BytesOut)/float64(inv.BytesIn),
			metric.WithAttributes(ratioAttrs...))
	}

	// thlibo.fallbacks{reason}
	if inv.Fallback != "" {
		r.fallbacks.Add(ctx, 1, metric.WithAttributes(
			attribute.String("reason", inv.Fallback),
		))
	}
}

func (r *otelRecorder) recordEvent(ctx context.Context, inv Invocation) {
	var rec otellog.Record
	rec.SetEventName(EventName)
	rec.SetBody(otellog.StringValue(EventName))
	attrs := []otellog.KeyValue{
		otellog.String("path", inv.Path),
		otellog.String("outcome", inv.Outcome),
		otellog.Int("bytes_in", inv.BytesIn),
		otellog.Int("bytes_out", inv.BytesOut),
		otellog.Int64("duration_ms", inv.Duration.Milliseconds()),
	}
	if inv.Processor != "" {
		attrs = append(attrs, otellog.String("processor", inv.Processor))
	}
	if inv.Kind != "" {
		attrs = append(attrs, otellog.String("kind", inv.Kind))
	}
	if inv.Fallback != "" {
		attrs = append(attrs, otellog.String("reason", inv.Fallback))
	}
	rec.AddAttributes(attrs...)
	r.logger.Emit(ctx, rec)
}

// Shutdown force-flushes and releases both providers, bounded by
// FlushTimeout. Best-effort: errors are joined and returned for the
// caller to log, but the caller ignores them (fail open). A dead
// collector makes this return at the timeout, not hang.
func (r *otelRecorder) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, FlushTimeout)
	defer cancel()

	var err error
	if r.mp != nil {
		err = shutdownMeter(ctx, r.mp)
	}
	if r.lp != nil {
		if e := r.lp.Shutdown(ctx); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// shutdownMeter force-flushes then shuts a MeterProvider down within
// the already-bounded ctx.
func shutdownMeter(ctx context.Context, mp *sdkmetric.MeterProvider) error {
	// ForceFlush pushes the pending delta datapoints; Shutdown then
	// flushes again and releases. Either can fail against a dead
	// collector; we return the first error and move on.
	if err := mp.ForceFlush(ctx); err != nil {
		_ = mp.Shutdown(ctx)
		return err
	}
	return mp.Shutdown(ctx)
}

// (compile-time assertions)
var (
	_ Recorder = (*otelRecorder)(nil)
	_ Recorder = noop{}
)
