---
type: Pipeline
title: Fuzz fuzz
description: "CI job fuzz ${{ matrix.target }} in the Fuzz workflow, 5 steps"
attributes:
  - { name: job, value: "fuzz ${{ matrix.target }}" }
  - { name: needs, value: budget }
  - { name: permissions, value: contents:read }
  - { name: runner, value: ubuntu-latest }
  - { name: runs, value: Checkout → Set up Go → Restore fuzz corpus → Fuzz → Collect crashers }
  - { name: steps, value: "5" }
  - { name: workflow, value: Fuzz }
edges:
  - { kind: configures, to: ../references/github-actions-actions-cache.md, confidence: extracted, source: .github/workflows/fuzz.yml }
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/fuzz.yml }
  - { kind: configures, to: ../references/github-actions-actions-setup-go.md, confidence: extracted, source: .github/workflows/fuzz.yml }
  - { kind: configures, to: ../references/github-actions-actions-upload-artifact.md, confidence: extracted, source: .github/workflows/fuzz.yml }
---
# Fuzz fuzz

<!-- signpost:managed:summary -->
CI job fuzz ${{ matrix.target }} in the Fuzz workflow, 5 steps
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/fuzz.yml`

- **Configures**: [actions/cache](../references/github-actions-actions-cache.md), [actions/checkout](../references/github-actions-actions-checkout.md), [actions/setup-go](../references/github-actions-actions-setup-go.md), [actions/upload-artifact](../references/github-actions-actions-upload-artifact.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
