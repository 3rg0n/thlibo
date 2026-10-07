---
type: Module
title: cmd/thlibo/compresscmd
description: 2 go files; 2 exported symbols.
attributes:
  - { name: exported, value: "2" }
  - { name: files, value: "2" }
  - { name: package, value: compresscmd }
edges:
  - { kind: imports, to: ./inferd.md, confidence: extracted, weight: 1, source: cmd/thlibo/compresscmd/compress.go }
  - { kind: imports, to: ./middleware.md, confidence: extracted, weight: 1, source: cmd/thlibo/compresscmd/compress.go }
  - { kind: imports, to: ./processors-1lzwtfn.md, confidence: extracted, weight: 1, source: cmd/thlibo/compresscmd/compress.go }
  - { kind: imports, to: ./router.md, confidence: extracted, weight: 1, source: cmd/thlibo/compresscmd/compress.go }
  - { kind: imports, to: ./telemetry.md, confidence: extracted, weight: 1, source: cmd/thlibo/compresscmd/compress.go }
---
# cmd/thlibo/compresscmd

<!-- signpost:managed:summary -->
2 go files; 2 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `cmd/thlibo/compresscmd/compress.go`
- `cmd/thlibo/compresscmd/compress_test.go`

- **Exports** (2): `BuildPipeline`, `Run`

- **Imports**: [internal/inferd](./inferd.md) ×1, [internal/middleware](./middleware.md) ×1, [internal/processors](./processors-1lzwtfn.md) ×1, [internal/router](./router.md) ×1, [internal/telemetry](./telemetry.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
