---
type: Pipeline
title: CI test
description: "CI job test (${{ matrix.os }}, Go ${{ matrix.go }}) in the CI workflow, 7 steps; runs on a pull request or a default-branch push"
tags: [gate]
attributes:
  - { name: job, value: "test (${{ matrix.os }}, Go ${{ matrix.go }})" }
  - { name: runner, value: "${{ matrix.os }}" }
  - { name: runs, value: Checkout → Set up Go → Set up Python → go vet → go build → go test → python processor tests }
  - { name: steps, value: "7" }
  - { name: workflow, value: CI }
edges:
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/ci.yml }
  - { kind: configures, to: ../references/github-actions-actions-setup-go.md, confidence: extracted, source: .github/workflows/ci.yml }
  - { kind: configures, to: ../references/github-actions-actions-setup-python.md, confidence: extracted, source: .github/workflows/ci.yml }
---
# CI test

<!-- signpost:managed:summary -->
CI job test (${{ matrix.os }}, Go ${{ matrix.go }}) in the CI workflow, 7 steps; runs on a pull request or a default-branch push
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/ci.yml`

- **Configures**: [actions/checkout](../references/github-actions-actions-checkout.md), [actions/setup-go](../references/github-actions-actions-setup-go.md), [actions/setup-python](../references/github-actions-actions-setup-python.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
