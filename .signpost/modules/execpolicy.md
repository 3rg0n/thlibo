---
type: Module
title: internal/execpolicy
description: 2 go files; 8 exported symbols.
attributes:
  - { name: exported, value: "8" }
  - { name: files, value: "2" }
  - { name: package, value: execpolicy }
edges:
  - { kind: imports, to: ../references/go-gopkg-in-yaml-v3.md, confidence: extracted, weight: 1, source: internal/execpolicy/execpolicy.go }
---
# internal/execpolicy

<!-- signpost:managed:summary -->
2 go files; 8 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `internal/execpolicy/execpolicy.go`
- `internal/execpolicy/execpolicy_test.go`

- **Exports** (8): `Decision`, `DecisionAllow`, `DecisionDeny`, `DefaultPath`, `ErrDenied`, `Load`, `Policy`, `Policy.Evaluate`

- **Imports**: [gopkg.in/yaml.v3](../references/go-gopkg-in-yaml-v3.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
