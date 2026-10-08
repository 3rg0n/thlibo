---
type: Module
title: internal/adapters/cursor
description: "5 go files; 7 exported symbols; entrypoint #!."
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: "#!" }
  - { name: exported, value: "7" }
  - { name: files, value: "5" }
  - { name: package, value: cursor }
edges:
  - { kind: imports, to: ./hookpath.md, confidence: extracted, weight: 2, source: internal/adapters/cursor/cursor.go }
---
# internal/adapters/cursor

<!-- signpost:managed:summary -->
5 go files; 7 exported symbols; entrypoint #!.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
5 files:
- `internal/adapters/cursor/cursor.go`
- `internal/adapters/cursor/cursor_test.go`
- `internal/adapters/cursor/hook-read.sh`
- `internal/adapters/cursor/hook.sh`
- `internal/adapters/cursor/remove_test.go`

- **Exports** (7): `DefaultHooksPath`, `HookScript`, `MergeHooksJSON`, `ReadHookScript`, `RemoveHooks`, `WriteHookScript`, `WriteReadHookScript`

- **Imports**: [internal/adapters/hookpath](./hookpath.md) ×2
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
