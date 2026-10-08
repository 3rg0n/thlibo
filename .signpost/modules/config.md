---
type: Module
title: internal/config
description: 2 go files; 5 exported symbols.
attributes:
  - { name: exported, value: "5" }
  - { name: files, value: "2" }
  - { name: package, value: config }
edges:
  - { kind: imports, to: ../references/go-gopkg-in-yaml-v3.md, confidence: extracted, weight: 1, source: internal/config/config.go }
---
# internal/config

<!-- signpost:managed:summary -->
2 go files; 5 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `internal/config/config.go`
- `internal/config/config_test.go`

- **Exports** (5): `Config`, `Config.MatchesAutoShorthandPath`, `Defaults`, `Load`, `Path`

- **Imports**: [gopkg.in/yaml.v3](../references/go-gopkg-in-yaml-v3.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
