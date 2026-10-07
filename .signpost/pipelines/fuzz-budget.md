---
type: Pipeline
title: Fuzz budget
description: "CI job budget in the Fuzz workflow, 1 step"
attributes:
  - { name: job, value: budget }
  - { name: permissions, value: contents:read }
  - { name: runner, value: ubuntu-latest }
  - { name: runs, value: set -euo pipefail }
  - { name: steps, value: "1" }
  - { name: workflow, value: Fuzz }
edges:
  - { kind: precedes, to: ./fuzz-fuzz.md, confidence: extracted, source: .github/workflows/fuzz.yml }
---
# Fuzz budget

<!-- signpost:managed:summary -->
CI job budget in the Fuzz workflow, 1 step
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/fuzz.yml`

- **Runs before**: [Fuzz fuzz](./fuzz-fuzz.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
