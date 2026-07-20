package telemetry

import (
	"context"
	"errors"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"

	mexphttp "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	mexpgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	stdoutmetric "go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"

	lexphttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	lexpgrpc "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	stdoutlog "go.opentelemetry.io/otel/exporters/stdout/stdoutlog"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/3rg0n/thlibo/internal/version"
)

// Init returns a Recorder. When telemetry is disabled (the default) or
// no signal exporter is configured, it returns the no-op recorder and
// no SDK is constructed. When enabled, it builds the metric + log
// providers from the standard OTEL_* environment variables and returns
// an OTel-backed recorder.
//
// Init NEVER returns an error and NEVER blocks: any construction
// failure falls open to the no-op recorder (ADR 0006, 0011). The
// caller wires the returned Recorder into the middleware and defers
// Shutdown; a disabled/failed recorder makes both a no-op.
func Init(ctx context.Context) Recorder {
	if !Enabled() {
		return noop{}
	}

	res := buildResource(ctx)

	mp := buildMeterProvider(ctx, res)
	lp := buildLoggerProvider(ctx, res)

	// If neither signal produced a provider, there's nothing to emit —
	// fall open rather than hold a half-built recorder.
	if mp == nil && lp == nil {
		return noop{}
	}

	rec := &otelRecorder{mp: mp, lp: lp}
	if mp != nil {
		if err := rec.initInstruments(mp.Meter("github.com/3rg0n/thlibo")); err != nil {
			// Instruments failed to register — drop metrics, keep events
			// if present; if nothing is left, fall open.
			rec.mp = nil
			_ = shutdownMeter(ctx, mp)
			if rec.lp == nil {
				return noop{}
			}
		}
	}
	if lp != nil {
		rec.logger = lp.Logger("github.com/3rg0n/thlibo")
	}
	return rec
}

// buildResource assembles the OTel resource. service.name defaults to
// "thlibo" (overridable by OTEL_SERVICE_NAME, which resource.Default
// already honours) and service.version carries the build tag. Custom
// OTEL_RESOURCE_ATTRIBUTES are merged by resource.Default().
func buildResource(ctx context.Context) *resource.Resource {
	attrs := []attribute.KeyValue{semconv.ServiceVersion(version.Tag)}
	if os.Getenv("OTEL_SERVICE_NAME") == "" &&
		!strings.Contains(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), "service.name") {
		attrs = append(attrs, semconv.ServiceName("thlibo"))
	}
	// Merge our attrs over the env-derived default resource. On error
	// (schema conflict), fall back to just our attributes.
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		res, _ = resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	}
	return res
}

// buildMeterProvider constructs a MeterProvider whose PeriodicReader
// wraps an env-selected exporter with DELTA temporality (each
// short-lived process emits a self-contained datapoint; ADR 0011).
// Returns nil if metrics are disabled (OTEL_METRICS_EXPORTER=none) or
// the exporter can't be built.
func buildMeterProvider(ctx context.Context, res *resource.Resource) *sdkmetric.MeterProvider {
	exp := buildMetricExporter(ctx)
	if exp == nil {
		return nil
	}
	// The PeriodicReader's 60s interval never fires for our short-lived
	// processes; we rely on the ForceFlush in Shutdown. The interval is
	// harmless (the process exits first) but we keep the default.
	reader := sdkmetric.NewPeriodicReader(exp)
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)
}

// buildMetricExporter selects a metric exporter from
// OTEL_METRICS_EXPORTER (otlp|console|none; default otlp), with OTLP
// transport from OTEL_EXPORTER_OTLP[_METRICS]_PROTOCOL. Delta
// temporality is forced. Returns nil on "none" or any build error.
func buildMetricExporter(ctx context.Context) sdkmetric.Exporter {
	switch exporterChoice("OTEL_METRICS_EXPORTER") {
	case "none":
		return nil
	case "console":
		// Debug-only path; emits cumulative (the stdout exporter has no
		// temporality selector). The production OTLP path below is delta.
		exp, err := stdoutmetric.New()
		if err != nil {
			return nil
		}
		return exp
	default: // otlp
		if useGRPC("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL") {
			exp, err := mexpgrpc.New(ctx, mexpgrpc.WithTemporalitySelector(deltaSelector))
			if err != nil {
				return nil
			}
			return exp
		}
		exp, err := mexphttp.New(ctx, mexphttp.WithTemporalitySelector(deltaSelector))
		if err != nil {
			return nil
		}
		return exp
	}
}

// deltaSelector forces delta aggregation temporality for every
// instrument kind — the correct choice for short-lived processes.
func deltaSelector(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

// buildLoggerProvider constructs a LoggerProvider for the per-
// invocation event, from OTEL_LOGS_EXPORTER (otlp|console|none;
// default otlp). Returns nil if logs are disabled or the exporter
// can't be built. NOTE: the OTel logs SDK is pre-1.0 (ADR 0011).
func buildLoggerProvider(ctx context.Context, res *resource.Resource) *sdklog.LoggerProvider {
	exp := buildLogExporter(ctx)
	if exp == nil {
		return nil
	}
	return sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
	)
}

func buildLogExporter(ctx context.Context) sdklog.Exporter {
	switch exporterChoice("OTEL_LOGS_EXPORTER") {
	case "none":
		return nil
	case "console":
		exp, err := stdoutlog.New()
		if err != nil {
			return nil
		}
		return exp
	default: // otlp
		if useGRPC("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL") {
			exp, err := lexpgrpc.New(ctx)
			if err != nil {
				return nil
			}
			return exp
		}
		exp, err := lexphttp.New(ctx)
		if err != nil {
			return nil
		}
		return exp
	}
}

// exporterChoice reads a signal-specific exporter selector, defaulting
// to "otlp" (matching Claude Code / the OTel default when telemetry is
// on). Only the first comma-separated value is honoured.
func exporterChoice(key string) string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return "otlp"
	}
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// useGRPC reports whether the OTLP protocol resolves to grpc. Checks
// the signal-specific protocol var, then the general one; default is
// http/protobuf per the OTel spec.
func useGRPC(signalKey string) bool {
	p := strings.ToLower(strings.TrimSpace(os.Getenv(signalKey)))
	if p == "" {
		p = strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")))
	}
	return p == "grpc"
}

// otelRecorder is the enabled Recorder. It holds the six metric
// instruments and the event logger. Any nil sub-provider means that
// signal was disabled or failed; recording against it is skipped.
type otelRecorder struct {
	mp *sdkmetric.MeterProvider
	lp *sdklog.LoggerProvider

	logger otellog.Logger

	invocations metric.Int64Counter
	bytesProc   metric.Int64Counter
	bytesSaved  metric.Int64Counter
	ratio       metric.Float64Histogram
	dispatchDur metric.Float64Histogram
	fallbacks   metric.Int64Counter
}

func (r *otelRecorder) initInstruments(m metric.Meter) error {
	var err error
	var errs []error
	r.invocations, err = m.Int64Counter(MetricNamespace+"invocations",
		metric.WithUnit("{invocation}"), metric.WithDescription("thlibo middleware invocations"))
	errs = append(errs, err)
	r.bytesProc, err = m.Int64Counter(MetricNamespace+"bytes.processed",
		metric.WithUnit("By"), metric.WithDescription("bytes in/out of the middleware"))
	errs = append(errs, err)
	r.bytesSaved, err = m.Int64Counter(MetricNamespace+"bytes.saved",
		metric.WithUnit("By"), metric.WithDescription("bytes removed by compression (input-output)"))
	errs = append(errs, err)
	r.ratio, err = m.Float64Histogram(MetricNamespace+"compression.ratio",
		metric.WithUnit("1"), metric.WithDescription("output/input size ratio"))
	errs = append(errs, err)
	r.dispatchDur, err = m.Float64Histogram(MetricNamespace+"dispatch.duration",
		metric.WithUnit("s"), metric.WithDescription("middleware decide+dispatch duration"))
	errs = append(errs, err)
	r.fallbacks, err = m.Int64Counter(MetricNamespace+"fallbacks",
		metric.WithUnit("{fallback}"), metric.WithDescription("fail-open fallbacks by reason"))
	errs = append(errs, err)
	return errors.Join(errs...)
}
