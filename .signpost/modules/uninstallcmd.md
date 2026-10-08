---
type: Module
title: cmd/thlibo/uninstallcmd
description: 2 go files; 4 exported symbols.
attributes:
  - { name: exported, value: "4" }
  - { name: files, value: "2" }
  - { name: package, value: uninstallcmd }
edges:
  - { kind: imports, to: ./claudecode.md, confidence: extracted, weight: 1, source: cmd/thlibo/uninstallcmd/uninstall.go }
  - { kind: imports, to: ./codex.md, confidence: extracted, weight: 2, source: cmd/thlibo/uninstallcmd/uninstall.go }
  - { kind: imports, to: ./copilot.md, confidence: extracted, weight: 1, source: cmd/thlibo/uninstallcmd/uninstall.go }
  - { kind: imports, to: ./cursor.md, confidence: extracted, weight: 2, source: cmd/thlibo/uninstallcmd/uninstall.go }
  - { kind: imports, to: ./install.md, confidence: extracted, weight: 1, source: cmd/thlibo/uninstallcmd/uninstall.go }
---
# cmd/thlibo/uninstallcmd

<!-- signpost:managed:summary -->
2 go files; 4 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `cmd/thlibo/uninstallcmd/uninstall.go`
- `cmd/thlibo/uninstallcmd/uninstall_test.go`

- **Exports** (4): `ExitDirError`, `ExitOK`, `ExitUsage`, `Run`

- **Imports**: [internal/adapters/claudecode](./claudecode.md) ×1, [internal/adapters/codex](./codex.md) ×2, [internal/adapters/copilot](./copilot.md) ×1, [internal/adapters/cursor](./cursor.md) ×2, [internal/install](./install.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
