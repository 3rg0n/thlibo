---
type: Pipeline
title: Release release
description: "CI job release in the Release workflow, 7 steps"
attributes:
  - { name: job, value: release }
  - { name: needs, value: "build, verify-install" }
  - { name: permissions, value: "attestations:write, contents:write, id-token:write" }
  - { name: runner, value: ubuntu-latest }
  - { name: runs, value: Checkout → Download all artifacts → Generate SHA256 checksums → Generate SBOM (CycloneDX) → Install cosign → Sign artefacts with cosign (keyless) → Publish release }
  - { name: steps, value: "7" }
  - { name: workflow, value: Release }
edges:
  - { kind: configures, to: ../references/github-actions-actions-checkout.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-actions-download-artifact.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-anchore-sbom-action.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-sigstore-cosign-installer.md, confidence: extracted, source: .github/workflows/release.yml }
  - { kind: configures, to: ../references/github-actions-softprops-action-gh-release.md, confidence: extracted, source: .github/workflows/release.yml }
---
# Release release

<!-- signpost:managed:summary -->
CI job release in the Release workflow, 7 steps
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `.github/workflows/release.yml`

- **Configures**: [actions/checkout](../references/github-actions-actions-checkout.md), [actions/download-artifact](../references/github-actions-actions-download-artifact.md), [anchore/sbom-action](../references/github-actions-anchore-sbom-action.md), [sigstore/cosign-installer](../references/github-actions-sigstore-cosign-installer.md), [softprops/action-gh-release](../references/github-actions-softprops-action-gh-release.md)
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
