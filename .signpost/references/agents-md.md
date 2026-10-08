---
type: Document
title: AGENTS.md
description: "Stated constraints, 9 rules read from AGENTS.md."
tags: [agent-rules, constraint]
attributes:
  - { name: rules, value: "9" }
  - { name: sections, value: "AGENTS.md, AGENTS.md / Repository map" }
edges:
  - { kind: documents, to: ../modules/cargo-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/casecmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/casefile.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/claudecode.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/codex.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/compresscmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/config.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/configcmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/copilot.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/cordon-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/cursor.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/execcmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/execpolicy.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/git-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/go-test-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/inferd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/install.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/installcmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/lint-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/logx.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/middleware.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/ndjson-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/npm-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/pdf.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/pdf-to-md.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/pdfocr.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/processors-1j5jcrl.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/processors-1lzwtfn.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/promptsan.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/pytest-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/rewritecmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/router.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/scripts.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/shellcmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/shorthand.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/shorthandcmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/stacktrace-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/telemetry.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/thlibo.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/trivy-filter.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/uninstallcmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/update.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/upgradecmd.md, confidence: extracted, source: AGENTS.md }
  - { kind: documents, to: ../modules/version.md, confidence: extracted, source: AGENTS.md }
---
# AGENTS.md

<!-- signpost:managed:summary -->
Stated constraints, 9 rules read from AGENTS.md.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `AGENTS.md`

- **Documents**: [processors/cargo-filter](../modules/cargo-filter.md), [cmd/thlibo/casecmd](../modules/casecmd.md), [internal/casefile](../modules/casefile.md), [internal/adapters/claudecode](../modules/claudecode.md), [internal/adapters/codex](../modules/codex.md), [cmd/thlibo/compresscmd](../modules/compresscmd.md), [internal/config](../modules/config.md), [cmd/thlibo/configcmd](../modules/configcmd.md), [internal/adapters/copilot](../modules/copilot.md), [processors/cordon-filter](../modules/cordon-filter.md), [internal/adapters/cursor](../modules/cursor.md), [cmd/thlibo/execcmd](../modules/execcmd.md), [internal/execpolicy](../modules/execpolicy.md), [processors/git-filter](../modules/git-filter.md), [processors/go-test-filter](../modules/go-test-filter.md), [internal/inferd](../modules/inferd.md), [internal/install](../modules/install.md), [cmd/thlibo/installcmd](../modules/installcmd.md), [processors/lint-filter](../modules/lint-filter.md), [internal/logx](../modules/logx.md), [internal/middleware](../modules/middleware.md), [processors/ndjson-filter](../modules/ndjson-filter.md), [processors/npm-filter](../modules/npm-filter.md), [internal/pdf](../modules/pdf.md), [processors/pdf-to-md](../modules/pdf-to-md.md), [internal/pdfocr](../modules/pdfocr.md), [processors](../modules/processors-1j5jcrl.md), [internal/processors](../modules/processors-1lzwtfn.md), [internal/promptsan](../modules/promptsan.md), [processors/pytest-filter](../modules/pytest-filter.md), [cmd/thlibo/rewritecmd](../modules/rewritecmd.md), [internal/router](../modules/router.md), [scripts](../modules/scripts.md), [internal/shellcmd](../modules/shellcmd.md), [internal/shorthand](../modules/shorthand.md), [cmd/thlibo/shorthandcmd](../modules/shorthandcmd.md), [processors/stacktrace-filter](../modules/stacktrace-filter.md), [internal/telemetry](../modules/telemetry.md), [cmd/thlibo](../modules/thlibo.md), [processors/trivy-filter](../modules/trivy-filter.md), [cmd/thlibo/uninstallcmd](../modules/uninstallcmd.md), [internal/update](../modules/update.md), [cmd/thlibo/upgradecmd](../modules/upgradecmd.md), [internal/version](../modules/version.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
