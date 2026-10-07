---
type: Module
title: internal/update
description: 12 go files; 15 exported symbols.
attributes:
  - { name: exported, value: "15" }
  - { name: files, value: "12" }
  - { name: package, value: update }
edges:
  - { kind: imports, to: ./logx.md, confidence: extracted, weight: 1, source: internal/update/runner.go }
  - { kind: imports, to: ../references/go-golang-org-x-term.md, confidence: extracted, weight: 2, source: internal/update/headless_unix.go }
---
# internal/update

<!-- signpost:managed:summary -->
12 go files; 15 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
12 files:
- `internal/update/check.go`
- `internal/update/check_test.go`
- `internal/update/headless.go`
- `internal/update/headless_test.go`
- `internal/update/headless_unix.go`
- `internal/update/headless_windows.go`
- `internal/update/runner.go`
- `internal/update/runner_test.go`
- `internal/update/toast_darwin.go`
- `internal/update/toast_linux.go`
- `internal/update/toast_linux_test.go`
- `internal/update/toast_other.go`

- **Exports** (15): `Check`, `Decision`, `DefaultInterval`, `DefaultReleaseAPI`, `DefaultTimeout`, `ErrDevBuild`, `Fetcher`, `HTTPFetcher`, `HTTPFetcher.Fetch`, `IsHeadless`, `NewHTTPFetcher`, `NoticeLine`, `Runner`, `Runner.Run`, `UserAgent`

- **Imports**: [internal/logx](./logx.md) ×1, [golang.org/x/term](../references/go-golang-org-x-term.md) ×2
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
