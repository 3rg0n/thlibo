---
type: Pipeline
title: Release verify-install
description: "CI job verify-install (${{ matrix.os }}) in the Release workflow, 4 steps"
attributes:
  - { name: job, value: "verify-install (${{ matrix.os }})" }
  - { name: needs, value: build }
  - { name: permissions, value: "attestations:write, contents:write, id-token:write" }
  - { name: runner, value: "${{ matrix.os }}" }
  - { name: runs, value: Checkout → Download archive artifact → Run install.sh against local archive (linux) → Run install.ps1 against local archive (windows) }
  - { name: steps, value: "4" }
  - { name: workflow, value: Release }
edges:
  - { kind: precedes, to: ./release-release.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-actions-download-artifact.md, confidence: extracted, source: .github/workflows/release.yml }
---
# Release verify-install

<!-- signpost:managed:summary -->
CI job verify-install (${{ matrix.os }}) in the Release workflow, 4 steps
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/release.yml`

- **Configures**: [actions/checkout](../references/github-actions-actions-checkout.md), [actions/download-artifact](../references/github-actions-actions-download-artifact.md)

- **Runs before**: [Release release](./release-release.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
