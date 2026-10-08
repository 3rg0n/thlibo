---
type: Module
title: internal/shorthand
description: 4 go files; 10 exported symbols.
attributes:
  - { name: exported, value: "10" }
  - { name: files, value: "4" }
  - { name: package, value: shorthand }
edges:
  - { kind: imports, to: ../references/go-gopkg-in-yaml-v3.md, confidence: extracted, weight: 1, source: internal/shorthand/yaml.go }
---
# internal/shorthand

<!-- signpost:managed:summary -->
4 go files; 10 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
4 files:
- `internal/shorthand/shorthand.go`
- `internal/shorthand/shorthand_test.go`
- `internal/shorthand/yaml.go`
- `internal/shorthand/yaml_test.go`

- **Exports** (10): `Backend`, `Engine`, `Engine.Compress`, `Engine.CompressYAML`, `ErrBackendUnavailable`, `Evaluate`, `IsYAMLContent`, `MinReductionPercent`, `Result`, `Result.Safe`

- **Imports**: [gopkg.in/yaml.v3](../references/go-gopkg-in-yaml-v3.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
