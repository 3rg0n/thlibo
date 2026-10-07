---
type: Module
title: cmd/thlibo/shorthandcmd
description: 5 go files; 8 exported symbols.
attributes:
  - { name: exported, value: "8" }
  - { name: files, value: "5" }
  - { name: package, value: shorthandcmd }
edges:
  - { kind: imports, to: ./compresscmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/shorthandcmd/shorthand.go }
  - { kind: imports, to: ./config.md, confidence: extracted, weight: 2, source: cmd/thlibo/shorthandcmd/writehook.go }
  - { kind: imports, to: ./inferd.md, confidence: extracted, weight: 1, source: cmd/thlibo/shorthandcmd/shorthand.go }
  - { kind: imports, to: ./processors-1lzwtfn.md, confidence: extracted, weight: 1, source: cmd/thlibo/shorthandcmd/shorthand.go }
  - { kind: imports, to: ./shorthand.md, confidence: extracted, weight: 2, source: cmd/thlibo/shorthandcmd/shorthand.go }
---
# cmd/thlibo/shorthandcmd

<!-- signpost:managed:summary -->
5 go files; 8 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
5 files:
- `cmd/thlibo/shorthandcmd/shorthand.go`
- `cmd/thlibo/shorthandcmd/shorthand_test.go`
- `cmd/thlibo/shorthandcmd/writehook.go`
- `cmd/thlibo/shorthandcmd/writehook_gates_test.go`
- `cmd/thlibo/shorthandcmd/writehook_test.go`

- **Exports** (8): `ExitBackendDown`, `ExitOK`, `ExitReadFailed`, `ExitUsage`, `ExitValidateFailed`, `ExitWriteFailed`, `Run`, `RunWriteHook`

- **Imports**: [cmd/thlibo/compresscmd](./compresscmd.md) ×1, [internal/config](./config.md) ×2, [internal/inferd](./inferd.md) ×1, [internal/processors](./processors-1lzwtfn.md) ×1, [internal/shorthand](./shorthand.md) ×2
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
