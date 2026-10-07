---
type: Module
title: internal/adapters/copilot
description: "7 go files; 12 exported symbols; entrypoint #!."
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: "#!" }
  - { name: exported, value: "12" }
  - { name: files, value: "7" }
  - { name: package, value: copilot }
---
# internal/adapters/copilot

<!-- signpost:managed:summary -->
7 go files; 12 exported symbols; entrypoint #!.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
7 files:
- `internal/adapters/copilot/copilot.go`
- `internal/adapters/copilot/copilot_test.go`
- `internal/adapters/copilot/encoding_test.go`
- `internal/adapters/copilot/hook-post.ps1`
- `internal/adapters/copilot/hook-post.sh`
- `internal/adapters/copilot/hook-pre.ps1`
- `internal/adapters/copilot/hook-pre.sh`

- **Exports** (12): `HookTimeoutSec`, `PostHookPS1`, `PostHookPS1Name`, `PostHookSh`, `PostHookShName`, `PreHookPS1`, `PreHookPS1Name`, `PreHookSh`, `PreHookShName`, `RemoveHooks`, `WriteHookScripts`, `WriteHooksJSON`
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
