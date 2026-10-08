---
type: Pipeline
title: CI secret-scanning
description: "CI job secret-scanning in the CI workflow, 2 steps; runs on a pull request or a default-branch push"
tags: [gate]
attributes:
  - { name: job, value: secret-scanning }
  - { name: runner, value: ubuntu-latest }
  - { name: runs, value: Checkout → gitleaks }
  - { name: steps, value: "2" }
  - { name: workflow, value: CI }
edges:
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/ci.yml }
  - { kind: configures, to: ../references/github-actions-gitleaks-gitleaks-action.md, confidence: extracted, source: .github/workflows/ci.yml }
---
# CI secret-scanning

<!-- signpost:managed:summary -->
CI job secret-scanning in the CI workflow, 2 steps; runs on a pull request or a default-branch push
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/ci.yml`

- **Configures**: [actions/checkout](../references/github-actions-actions-checkout.md), [gitleaks/gitleaks-action](../references/github-actions-gitleaks-gitleaks-action.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
