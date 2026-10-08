---
type: Module
title: internal/install
description: 22 go files; 17 exported symbols.
attributes:
  - { name: exported, value: "17" }
  - { name: files, value: "22" }
  - { name: package, value: install }
edges:
  - { kind: imports, to: ./processors-1j5jcrl.md, confidence: extracted, weight: 2, source: internal/install/mirror.go }
---
# internal/install

<!-- signpost:managed:summary -->
22 go files; 17 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
22 files:
- `internal/install/autostart.go`
- `internal/install/autostart_darwin.go`
- `internal/install/autostart_linux.go`
- `internal/install/autostart_linux_test.go`
- `internal/install/autostart_stub_darwin.go`
- `internal/install/autostart_stub_linux.go`
- `internal/install/autostart_stub_windows.go`
- `internal/install/autostart_windows.go`
- `internal/install/autostart_windows_test.go`
- `internal/install/extract_test.go`
- `internal/install/inferd.go`
- `internal/install/inferd_asset_test.go`
- `internal/install/inferd_backends_test.go`
- `internal/install/inferd_pipe_unix.go`
- `internal/install/inferd_pipe_windows.go`
- `internal/install/inferd_probe_test.go`
- `internal/install/inferd_version_test.go`
- `internal/install/install.go`
- `internal/install/migrate_v05.go`
- `internal/install/migrate_v05_test.go`
- `internal/install/mirror.go`
- `internal/install/mirror_test.go`

- **Exports** (17): `AutostartSpec`, `DefaultProcessorsDir`, `ErrInferdNeedsManualStep`, `ErrInferdUnsupported`, `InferdInstallResult`, `InferdInstallSpec`, `InstallInferd`, `Installer`, `MigrateFromV05`, `MigrateResult`, `MigrateResult.HasWork`, `MinInferdVersion`, `MirrorBuiltins`, `NewInstaller`, `ProgressFunc`, `PullOptions`, `SharedModelsDir`

- **Imports**: [processors](./processors-1j5jcrl.md) ×2
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
