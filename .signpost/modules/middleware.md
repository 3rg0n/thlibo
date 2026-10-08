---
type: Module
title: internal/middleware
description: 13 go files; 9 exported symbols.
attributes:
  - { name: exported, value: "9" }
  - { name: files, value: "13" }
  - { name: package, value: middleware }
edges:
  - { kind: imports, to: ./inferd.md, confidence: extracted, weight: 2, source: internal/middleware/cordon_fallback_test.go }
  - { kind: imports, to: ./processors-1j5jcrl.md, confidence: extracted, weight: 5, source: internal/middleware/builtins_test.go }
  - { kind: imports, to: ./processors-1lzwtfn.md, confidence: extracted, weight: 10, source: internal/middleware/builtins_test.go }
  - { kind: imports, to: ./promptsan.md, confidence: extracted, weight: 1, source: internal/middleware/middleware.go }
  - { kind: imports, to: ./router.md, confidence: extracted, weight: 4, source: internal/middleware/fallback_test.go }
  - { kind: imports, to: ./telemetry.md, confidence: extracted, weight: 2, source: internal/middleware/middleware.go }
---
# internal/middleware

<!-- signpost:managed:summary -->
13 go files; 9 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
13 files:
- `internal/middleware/binary_passthrough_test.go`
- `internal/middleware/builtins_test.go`
- `internal/middleware/cordon_fallback_test.go`
- `internal/middleware/embed_test.go`
- `internal/middleware/empty_output_test.go`
- `internal/middleware/fallback_test.go`
- `internal/middleware/family_dispatch_test.go`
- `internal/middleware/middleware.go`
- `internal/middleware/middleware_test.go`
- `internal/middleware/savings_test.go`
- `internal/middleware/structured_passthrough_test.go`
- `internal/middleware/telemetry_test.go`
- `internal/middleware/tokenest_test.go`

- **Exports** (9): `BuildRegistry`, `CordonMinInputLines`, `MinBytesForRouting`, `Pipeline`, `Pipeline.Process`, `Pipeline.Shutdown`, `PromptRunner`, `PromptRunner.Run`, `RouterClient`

- **Imports**: [internal/inferd](./inferd.md) ×2, [processors](./processors-1j5jcrl.md) ×5, [internal/processors](./processors-1lzwtfn.md) ×10, [internal/promptsan](./promptsan.md) ×1, [internal/router](./router.md) ×4, [internal/telemetry](./telemetry.md) ×2
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
