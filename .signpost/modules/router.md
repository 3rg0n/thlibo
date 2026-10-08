---
type: Module
title: internal/router
description: 2 go files; 7 exported symbols.
attributes:
  - { name: exported, value: "7" }
  - { name: files, value: "2" }
  - { name: package, value: router }
edges:
  - { kind: imports, to: ./inferd.md, confidence: extracted, weight: 2, source: internal/router/router.go }
  - { kind: imports, to: ./processors-1lzwtfn.md, confidence: extracted, weight: 2, source: internal/router/router.go }
  - { kind: imports, to: ./promptsan.md, confidence: extracted, weight: 1, source: internal/router/router.go }
---
# internal/router

<!-- signpost:managed:summary -->
2 go files; 7 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `internal/router/router.go`
- `internal/router/router_test.go`

- **Exports** (7): `ClientAdapter`, `ClientAdapter.Ask`, `Decision`, `Decision.Passthrough`, `ErrDaemonUnreachable`, `ParseResult`, `RouteInput`

- **Imports**: [internal/inferd](./inferd.md) ×2, [internal/processors](./processors-1lzwtfn.md) ×2, [internal/promptsan](./promptsan.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
