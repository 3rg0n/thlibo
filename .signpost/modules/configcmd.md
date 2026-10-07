---
type: Module
title: cmd/thlibo/configcmd
description: 3 go files; 6 exported symbols.
attributes:
  - { name: exported, value: "6" }
  - { name: files, value: "3" }
  - { name: package, value: configcmd }
edges:
  - { kind: imports, to: ./config.md, confidence: extracted, weight: 3, source: cmd/thlibo/configcmd/config.go }
  - { kind: imports, to: ../references/go-gopkg-in-yaml-v3.md, confidence: extracted, weight: 1, source: cmd/thlibo/configcmd/config.go }
---
# cmd/thlibo/configcmd

<!-- signpost:managed:summary -->
3 go files; 6 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
3 files:
- `cmd/thlibo/configcmd/config.go`
- `cmd/thlibo/configcmd/config_test.go`
- `cmd/thlibo/configcmd/run_test.go`

- **Exports** (6): `ExitAborted`, `ExitInvalidSetting`, `ExitOK`, `ExitUsage`, `ExitWriteFailed`, `Run`

- **Imports**: [internal/config](./config.md) ×3, [gopkg.in/yaml.v3](../references/go-gopkg-in-yaml-v3.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
