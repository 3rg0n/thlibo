---
type: Pipeline
title: CI scanners
description: "CI job scanners in the CI workflow, 12 steps; runs on a pull request or a default-branch push"
tags: [gate]
attributes:
  - { name: job, value: scanners }
  - { name: runner, value: ubuntu-latest }
  - { name: runs, value: Checkout → Set up Go → gofmt → Install staticcheck → staticcheck → Install govulncheck → govulncheck (informational for stdlib) → Install gosec → +4 more }
  - { name: steps, value: "12" }
  - { name: workflow, value: CI }
edges:
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/ci.yml }
  - { kind: configures, to: ../references/github-actions-actions-setup-go.md, confidence: extracted, source: .github/workflows/ci.yml }
  - { kind: configures, to: ../references/github-actions-actions-setup-python.md, confidence: extracted, source: .github/workflows/ci.yml }
---
# CI scanners

<!-- signpost:managed:summary -->
CI job scanners in the CI workflow, 12 steps; runs on a pull request or a default-branch push
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/ci.yml`

- **Configures**: [actions/checkout](../references/github-actions-actions-checkout.md), [actions/setup-go](../references/github-actions-actions-setup-go.md), [actions/setup-python](../references/github-actions-actions-setup-python.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
