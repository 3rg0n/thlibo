---
type: Pipeline
title: Pages deploy
description: "CI job deploy in the Pages workflow, 4 steps; runs on a pull request or a default-branch push"
tags: [gate]
attributes:
  - { name: job, value: deploy }
  - { name: permissions, value: "contents:read, id-token:write, pages:write" }
  - { name: runner, value: ubuntu-latest }
  - { name: runs, value: Checkout → Configure Pages → Upload artifact → Deploy }
  - { name: steps, value: "4" }
  - { name: workflow, value: Pages }
edges:
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/pages.yml }
  - { kind: configures, to: ../references/github-actions-actions-configure-pages.md, confidence: extracted, source: .github/workflows/pages.yml }
  - { kind: configures, to: ../references/github-actions-actions-deploy-pages.md, confidence: extracted, source: .github/workflows/pages.yml }
  - { kind: configures, to: ../references/github-actions-actions-upload-pages-artifact.md, confidence: extracted, source: .github/workflows/pages.yml }
---
# Pages deploy

<!-- signpost:managed:summary -->
CI job deploy in the Pages workflow, 4 steps; runs on a pull request or a default-branch push
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/pages.yml`

- **Configures**: [actions/checkout](../references/github-actions-actions-checkout.md), [actions/configure-pages](../references/github-actions-actions-configure-pages.md), [actions/deploy-pages](../references/github-actions-actions-deploy-pages.md), [actions/upload-pages-artifact](../references/github-actions-actions-upload-pages-artifact.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
