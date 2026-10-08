---
type: Module
title: internal/adapters/claudecode
description: "12 go files; 26 exported symbols; entrypoint #!."
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: "#!" }
  - { name: exported, value: "26" }
  - { name: files, value: "12" }
  - { name: package, value: claudecode }
edges:
  - { kind: imports, to: ./hookpath.md, confidence: extracted, weight: 1, source: internal/adapters/claudecode/claudecode.go }
---
# internal/adapters/claudecode

<!-- signpost:managed:summary -->
12 go files; 26 exported symbols; entrypoint #!.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
12 files:
- `internal/adapters/claudecode/claudecode.go`
- `internal/adapters/claudecode/claudecode_test.go`
- `internal/adapters/claudecode/encoding_test.go`
- `internal/adapters/claudecode/hook-read.ps1`
- `internal/adapters/claudecode/hook-read.sh`
- `internal/adapters/claudecode/hook-write.ps1`
- `internal/adapters/claudecode/hook-write.sh`
- `internal/adapters/claudecode/hook.ps1`
- `internal/adapters/claudecode/hook.sh`
- `internal/adapters/claudecode/platform.go`
- `internal/adapters/claudecode/skill.go`
- `internal/adapters/claudecode/windowshook_test.go`

- **Exports** (26): `CaselogSkill`, `HookEntry`, `HookPaths`, `HookReadScript`, `HookReadScriptPS1`, `HookScript`, `HookScriptPS1`, `InstallCaselogSkill`, `MergeHooks`, `MergeSettings`, `MergeSettingsAll`, `MergeSettingsFull`, `MergeSettingsWithRead`, `RemoveHooks`, `WriteHookReadScript`, `WriteHookReadScriptPS1`, `WriteHookScript`, `WriteHookScriptPS1`, `WriteHookWriteScript`, `WriteHookWriteScriptPS1`, `WriteResult`, `WriteResult.String`, `WriteResultConflict`, `WriteResultCreated`, `WriteResultUnchanged`, `WriteResultUpdated`

- **Imports**: [internal/adapters/hookpath](./hookpath.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
