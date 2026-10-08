---
type: Pipeline
title: Release build
description: "CI job build (${{ matrix.goos }}-${{ matrix.goarch }}) in the Release workflow, 7 steps"
attributes:
  - { name: job, value: "build (${{ matrix.goos }}-${{ matrix.goarch }})" }
  - { name: permissions, value: "attestations:write, contents:write, id-token:write" }
  - { name: runner, value: ubuntu-latest }
  - { name: runs, value: Checkout → Set up Go → Build binary → Copy docs into bundle → Archive (tar.gz for Unix) → Archive (zip for Windows) → Upload artifact }
  - { name: steps, value: "7" }
  - { name: workflow, value: Release }
edges:
  - { kind: precedes, to: ./release-release.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: precedes, to: ./release-verify-install.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-actions-setup-go.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-actions-upload-artifact.md, confidence: extracted, source: .github/workflows/release.yml }
---
# Release build

<!-- signpost:managed:summary -->
CI job build (${{ matrix.goos }}-${{ matrix.goarch }}) in the Release workflow, 7 steps
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/release.yml`

- **Configures**: [actions/checkout](../references/github-actions-actions-checkout.md), [actions/setup-go](../references/github-actions-actions-setup-go.md), [actions/upload-artifact](../references/github-actions-actions-upload-artifact.md)

- **Runs before**: [Release release](./release-release.md), [Release verify-install](./release-verify-install.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
