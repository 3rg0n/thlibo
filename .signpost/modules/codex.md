---
type: Module
title: internal/adapters/codex
description: "8 go files; 17 exported symbols; entrypoint #!."
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: "#!" }
  - { name: exported, value: "17" }
  - { name: files, value: "8" }
  - { name: package, value: codex }
---
# internal/adapters/codex

<!-- signpost:managed:summary -->
8 go files; 17 exported symbols; entrypoint #!.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
8 files:
- `internal/adapters/codex/codex.go`
- `internal/adapters/codex/codex_test.go`
- `internal/adapters/codex/hook.ps1`
- `internal/adapters/codex/hook.sh`
- `internal/adapters/codex/platform.go`
- `internal/adapters/codex/remove_test.go`
- `internal/adapters/codex/representation_test.go`
- `internal/adapters/codex/windowshook_test.go`

- **Exports** (17): `DetectRepresentation`, `EnableHooksFeatureFlag`, `HookFileName`, `HookScript`, `HookScriptFor`, `HookScriptPS1`, `InstallHook`, `MergeConfigTOMLHook`, `MergeHooksJSONHook`, `RemoveConfigTOMLHook`, `RemoveHooks`, `RemoveStaleHooksJSON`, `RepHooksJSON`, `RepInline`, `Representation`, `Representation.String`, `WriteHookScript`
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
