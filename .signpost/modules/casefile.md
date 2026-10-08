---
type: Module
title: internal/casefile
description: 2 go files; 9 exported symbols.
attributes:
  - { name: exported, value: "9" }
  - { name: files, value: "2" }
  - { name: package, value: casefile }
edges:
  - { kind: imports, to: ./middleware.md, confidence: extracted, weight: 1, source: internal/casefile/casefile.go }
---
# internal/casefile

<!-- signpost:managed:summary -->
2 go files; 9 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `internal/casefile/casefile.go`
- `internal/casefile/casefile_test.go`

- **Exports** (9): `BasisBytes`, `BasisIncomparable`, `Create`, `DefaultCasesRoot`, `ErrSourceNotRegular`, `Meta`, `Options`, `Prune`, `Result`

- **Imports**: [internal/middleware](./middleware.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
