---
type: Module
title: internal/processors
description: 45 go files; 49 exported symbols; entrypoint init.
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: init }
  - { name: exported, value: "49" }
  - { name: files, value: "45" }
  - { name: package, value: processors }
edges:
  - { kind: imports, to: ./inferd.md, confidence: extracted, weight: 2, source: internal/processors/bench_test.go }
  - { kind: imports, to: ./pdf.md, confidence: extracted, weight: 2, source: internal/processors/filter_pdf.go }
  - { kind: imports, to: ./processors-1j5jcrl.md, confidence: extracted, weight: 1, source: internal/processors/structured_test.go }
  - { kind: imports, to: ../references/go-golang-org-x-net.md, confidence: extracted, weight: 2, source: internal/processors/filter_mhtml.go }
  - { kind: imports, to: ../references/go-gopkg-in-yaml-v3.md, confidence: extracted, weight: 2, source: internal/processors/descriptor.go }
---
# internal/processors

<!-- signpost:managed:summary -->
45 go files; 49 exported symbols; entrypoint init.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
45 files:
- `internal/processors/bench_test.go`
- `internal/processors/budget_norace_test.go`
- `internal/processors/budget_race_test.go`
- `internal/processors/cordon.go`
- `internal/processors/cordon_parity_test.go`
- `internal/processors/cordon_signature.go`
- `internal/processors/cordon_test.go`
- `internal/processors/descriptor.go`
- `internal/processors/descriptor_test.go`
- `internal/processors/dispatch.go`
- `internal/processors/filter_cargo.go`
- `internal/processors/filter_cargo_test.go`
- `internal/processors/filter_git.go`
- `internal/processors/filter_git_test.go`
- `internal/processors/filter_golden_test.go`
- `internal/processors/filter_gotest.go`
- `internal/processors/filter_gotest_test.go`
- `internal/processors/filter_har.go`
- `internal/processors/filter_har_test.go`
- `internal/processors/filter_lint.go`
- `internal/processors/filter_lint_test.go`
- `internal/processors/filter_mhtml.go`
- `internal/processors/filter_mhtml_test.go`
- `internal/processors/filter_ndjson.go`
- `internal/processors/filter_ndjson_test.go`
- `internal/processors/filter_npm.go`
- `internal/processors/filter_npm_test.go`
- `internal/processors/filter_pdf.go`
- `internal/processors/filter_pdf_test.go`
- `internal/processors/filter_pytest.go`
- `internal/processors/filter_pytest_test.go`
- `internal/processors/filter_stacktrace.go`
- `internal/processors/filter_stacktrace_test.go`
- `internal/processors/filter_trivy.go`
- `internal/processors/filter_trivy_test.go`
- `internal/processors/native.go`
- `internal/processors/native_test.go`
- `internal/processors/registry.go`
- `internal/processors/registry_test.go`
- `internal/processors/signature_test.go`
- and 5 more

- **Exports** (49): `BinaryLooking`, `Build`, `BuildFromDisk`, `BuildFromSources`, `Descriptor`, `Descriptor.EntryCommand`, `Descriptor.MatchIsSignature`, `Descriptor.MatchesFastPath`, `Descriptor.RouterEligible`, `Descriptor.RoutingBlurb`, `Dispatcher`, `Dispatcher.Run`, `Dispatcher.RunChain`, `EntryFingerprint`, `ErrEntrySwapped`, `FsReader`, `Kind`, `KindNative`, `KindPrompt`, `KindScript`, `LowValueSentinel`, `NativeCtxFilter`, `NativeFilter`, `Origin`, `OriginBuiltin`, `OriginSource`, `OriginSource.String`, `OriginUser`, `ParseMarkdown`, `ParseYAML`, `PromptRunner`, `RegisterNative`, `RegisterNativeCtx`, `Registry`, `Registry.Get`, `Registry.Len`, `Registry.MatchCommand`, `Registry.MatchCommandLine`, `Registry.MatchFastPath`, `Registry.Names`, `Registry.RoutableNames`, `RunNative`, `RunNativeCtx`, `ShadowWarning`, `ShadowWarning.Error`, `Source`, `Strip`, `StructuredDocument`, `WriteInput`

- **Imports**: [internal/inferd](./inferd.md) ×2, [internal/pdf](./pdf.md) ×2, [processors](./processors-1j5jcrl.md) ×1, [golang.org/x/net](../references/go-golang-org-x-net.md) ×2, [gopkg.in/yaml.v3](../references/go-gopkg-in-yaml-v3.md) ×2
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
