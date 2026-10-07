---
type: Module
title: cmd/thlibo
description: 2 go files; entrypoint main; package main.
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: main }
  - { name: exported, value: "0" }
  - { name: files, value: "2" }
  - { name: package, value: main }
edges:
  - { kind: imports, to: ./casecmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./compresscmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./configcmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./execcmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./installcmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./logx.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./rewritecmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./shorthandcmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./uninstallcmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./update.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./upgradecmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
  - { kind: imports, to: ./version.md, confidence: extracted, weight: 1, source: cmd/thlibo/main.go }
---
# cmd/thlibo

<!-- signpost:managed:summary -->
2 go files; entrypoint main; package main.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `cmd/thlibo/main.go`
- `cmd/thlibo/main_test.go`

- **Imports**: [cmd/thlibo/casecmd](./casecmd.md) ×1, [cmd/thlibo/compresscmd](./compresscmd.md) ×1, [cmd/thlibo/configcmd](./configcmd.md) ×1, [cmd/thlibo/execcmd](./execcmd.md) ×1, [cmd/thlibo/installcmd](./installcmd.md) ×1, [internal/logx](./logx.md) ×1, [cmd/thlibo/rewritecmd](./rewritecmd.md) ×1, [cmd/thlibo/shorthandcmd](./shorthandcmd.md) ×1, [cmd/thlibo/uninstallcmd](./uninstallcmd.md) ×1, [internal/update](./update.md) ×1, [cmd/thlibo/upgradecmd](./upgradecmd.md) ×1, [internal/version](./version.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
