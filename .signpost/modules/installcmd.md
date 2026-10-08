---
type: Module
title: cmd/thlibo/installcmd
description: 2 go files; 1 exported symbol.
attributes:
  - { name: exported, value: "1" }
  - { name: files, value: "2" }
  - { name: package, value: installcmd }
edges:
  - { kind: imports, to: ./claudecode.md, confidence: extracted, weight: 1, source: cmd/thlibo/installcmd/install.go }
  - { kind: imports, to: ./codex.md, confidence: extracted, weight: 2, source: cmd/thlibo/installcmd/install.go }
  - { kind: imports, to: ./copilot.md, confidence: extracted, weight: 1, source: cmd/thlibo/installcmd/install.go }
  - { kind: imports, to: ./cursor.md, confidence: extracted, weight: 1, source: cmd/thlibo/installcmd/install.go }
  - { kind: imports, to: ./install.md, confidence: extracted, weight: 2, source: cmd/thlibo/installcmd/install.go }
---
# cmd/thlibo/installcmd

<!-- signpost:managed:summary -->
2 go files; 1 exported symbol.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
2 files:
- `cmd/thlibo/installcmd/install.go`
- `cmd/thlibo/installcmd/install_test.go`

- **Exports** (1): `Run`

- **Imports**: [internal/adapters/claudecode](./claudecode.md) ×1, [internal/adapters/codex](./codex.md) ×2, [internal/adapters/copilot](./copilot.md) ×1, [internal/adapters/cursor](./cursor.md) ×1, [internal/install](./install.md) ×2
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
