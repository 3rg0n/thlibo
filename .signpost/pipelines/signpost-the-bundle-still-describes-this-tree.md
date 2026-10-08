---
type: Pipeline
title: signpost the bundle still describes this tree
description: "CI job the bundle still describes this tree in the signpost workflow, 3 steps; runs on a pull request or a default-branch push"
tags: [gate]
attributes:
  - { name: job, value: the bundle still describes this tree }
  - { name: permissions, value: contents:read }
  - { name: runner, value: ubuntu-24.04 }
  - { name: runs, value: actions/checkout → Install signpost → Verify the bundle }
  - { name: steps, value: "3" }
  - { name: workflow, value: signpost }
edges:
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/signpost.yml }
---
# signpost the bundle still describes this tree

<!-- signpost:managed:summary -->
CI job the bundle still describes this tree in the signpost workflow, 3 steps; runs on a pull request or a default-branch push
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/signpost.yml`

- **Configures**: [actions/checkout](../references/github-actions-actions-checkout.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
