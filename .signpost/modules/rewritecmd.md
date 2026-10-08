---
type: Module
title: cmd/thlibo/rewritecmd
description: 2 go files; 6 exported symbols.
attributes:
  - { name: exported, value: "6" }
  - { name: files, value: "2" }
  - { name: package, value: rewritecmd }
edges:
  - { kind: imports, to: ./middleware.md, confidence: extracted, weight: 1, source: cmd/thlibo/rewritecmd/rewrite.go }
  - { kind: imports, to: ./shellcmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/rewritecmd/rewrite.go }
---
# cmd/thlibo/rewritecmd

<!-- signpost:managed:summary -->
2 go files; 6 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `cmd/thlibo/rewritecmd/rewrite.go`
- `cmd/thlibo/rewritecmd/rewrite_test.go`

- **Exports** (6): `ExitAsk`, `ExitDeny`, `ExitInternal`, `ExitPassthrough`, `ExitRewrite`, `Run`

- **Imports**: [internal/middleware](./middleware.md) ×1, [internal/shellcmd](./shellcmd.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
