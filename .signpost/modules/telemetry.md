---
type: Module
title: internal/telemetry
description: 4 go files; 22 exported symbols.
attributes:
  - { name: exported, value: "22" }
  - { name: files, value: "4" }
  - { name: package, value: telemetry }
edges:
  - { kind: imports, to: ./version.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel.md, confidence: extracted, weight: 3, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-exporters-otlp-otlplog-otlploggrpc.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-exporters-otlp-otlplog-otlploghttp.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-exporters-otlp-otlpmetric-otlpmetricgrpc.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-exporters-otlp-otlpmetric-otlpmetrichttp.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-exporters-stdout-stdoutlog.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-exporters-stdout-stdoutmetric.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-log.md, confidence: extracted, weight: 2, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-metric.md, confidence: extracted, weight: 2, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-sdk.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-sdk-log.md, confidence: extracted, weight: 1, source: internal/telemetry/otel.go }
  - { kind: imports, to: ../references/go-go-opentelemetry-io-otel-sdk-metric.md, confidence: extracted, weight: 3, source: internal/telemetry/otel.go }
---
# internal/telemetry

<!-- signpost:managed:summary -->
4 go files; 22 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
4 files:
- `internal/telemetry/otel.go`
- `internal/telemetry/record.go`
- `internal/telemetry/telemetry.go`
- `internal/telemetry/telemetry_test.go`

- **Exports** (22): `Enabled`, `EventName`, `FlushTimeout`, `Init`, `Invocation`, `MetricNamespace`, `NoopRecorder`, `OutcomeCompressed`, `OutcomeFallback`, `OutcomePassthrough`, `PathFastPath`, `PathPassthrough`, `PathRouter`, `PathShortCircuit`, `ProcessorLabel`, `ReasonEmptyOutput`, `ReasonInferdUnreachable`, `ReasonRouterError`, `ReasonScriptError`, `ReasonTimeout`, `Recorder`, `UserProcessorLabel`

- **Imports**: [internal/version](./version.md) ×1, [go.opentelemetry.io/otel](../references/go-go-opentelemetry-io-otel.md) ×3, [go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc](../references/go-go-opentelemetry-io-otel-exporters-otlp-otlplog-otlploggrpc.md) ×1, [go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp](../references/go-go-opentelemetry-io-otel-exporters-otlp-otlplog-otlploghttp.md) ×1, [go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc](../references/go-go-opentelemetry-io-otel-exporters-otlp-otlpmetric-otlpmetricgrpc.md) ×1, [go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp](../references/go-go-opentelemetry-io-otel-exporters-otlp-otlpmetric-otlpmetrichttp.md) ×1, [go.opentelemetry.io/otel/exporters/stdout/stdoutlog](../references/go-go-opentelemetry-io-otel-exporters-stdout-stdoutlog.md) ×1, [go.opentelemetry.io/otel/exporters/stdout/stdoutmetric](../references/go-go-opentelemetry-io-otel-exporters-stdout-stdoutmetric.md) ×1, [go.opentelemetry.io/otel/log](../references/go-go-opentelemetry-io-otel-log.md) ×2, [go.opentelemetry.io/otel/metric](../references/go-go-opentelemetry-io-otel-metric.md) ×2, [go.opentelemetry.io/otel/sdk](../references/go-go-opentelemetry-io-otel-sdk.md) ×1, [go.opentelemetry.io/otel/sdk/log](../references/go-go-opentelemetry-io-otel-sdk-log.md) ×1, [go.opentelemetry.io/otel/sdk/metric](../references/go-go-opentelemetry-io-otel-sdk-metric.md) ×3
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
