---
type: Module
title: cmd/thlibo/execcmd
description: 4 go files; 5 exported symbols.
attributes:
  - { name: exported, value: "5" }
  - { name: files, value: "4" }
  - { name: package, value: execcmd }
edges:
  - { kind: imports, to: ./execpolicy.md, confidence: extracted, weight: 1, source: cmd/thlibo/execcmd/exec.go }
  - { kind: imports, to: ./inferd.md, confidence: extracted, weight: 2, source: cmd/thlibo/execcmd/exec.go }
  - { kind: imports, to: ./logx.md, confidence: extracted, weight: 1, source: cmd/thlibo/execcmd/exec.go }
  - { kind: imports, to: ./middleware.md, confidence: extracted, weight: 2, source: cmd/thlibo/execcmd/exec.go }
  - { kind: imports, to: ./processors-1j5jcrl.md, confidence: extracted, weight: 1, source: cmd/thlibo/execcmd/exec_test.go }
  - { kind: imports, to: ./processors-1lzwtfn.md, confidence: extracted, weight: 2, source: cmd/thlibo/execcmd/exec_test.go }
  - { kind: imports, to: ./router.md, confidence: extracted, weight: 2, source: cmd/thlibo/execcmd/exec.go }
  - { kind: imports, to: ./telemetry.md, confidence: extracted, weight: 1, source: cmd/thlibo/execcmd/exec.go }
---
# cmd/thlibo/execcmd

<!-- signpost:managed:summary -->
4 go files; 5 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
4 files:
- `cmd/thlibo/execcmd/exec.go`
- `cmd/thlibo/execcmd/exec_test.go`
- `cmd/thlibo/execcmd/pipeline.go`
- `cmd/thlibo/execcmd/wiring_test.go`

- **Exports** (5): `ExitChildSignaled`, `ExitPolicyDenied`, `ExitSpawnFailed`, `ExitUsage`, `Run`

- **Imports**: [internal/execpolicy](./execpolicy.md) ×1, [internal/inferd](./inferd.md) ×2, [internal/logx](./logx.md) ×1, [internal/middleware](./middleware.md) ×2, [processors](./processors-1j5jcrl.md) ×1, [internal/processors](./processors-1lzwtfn.md) ×2, [internal/router](./router.md) ×2, [internal/telemetry](./telemetry.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
