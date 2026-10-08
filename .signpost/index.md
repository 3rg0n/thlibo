---
okf_version: "0.2"
type: Index
title: Repository map
description: "Structural map of this repository: 122 concepts, 217 relationships."
---
# Repository map

<!-- signpost:managed:index -->
Start here. What the shape of this repository says, then a line per page naming what is on it.

### How work is done here

- [How work is done here](./practices.md) — what this repository declares about building, testing, gating, and ownership, and what it does not.

### Most connected

The places a wrong assumption propagates furthest, so the places to read first.

- [AGENTS.md](./references/agents-md.md) — 45 relationships (0 in, 45 out)
- [CLAUDE.md](./references/claude-md.md) — 45 relationships (0 in, 45 out)
- [internal/telemetry](./modules/telemetry.md) — 18 relationships (5 in, 13 out)
- [cmd/thlibo](./modules/thlibo.md) — 14 relationships (2 in, 12 out)
- [internal/middleware](./modules/middleware.md) — 12 relationships (6 in, 6 out)

### Structural findings

What the shape of this repository says. Each line is a result — where one reads "none", that is the finding.

- **Import cycles: none.** No module here imports its way back to itself.
- **Cross-cluster edges: 51.** Where a change is most likely to surprise someone: the two sides are maintained as separate concerns and coupled anyway.
  - [cmd/thlibo/casecmd](./modules/casecmd.md) → [cmd/thlibo/compresscmd](./modules/compresscmd.md) (imports)
  - [cmd/thlibo/casecmd](./modules/casecmd.md) → [internal/inferd](./modules/inferd.md) (imports)
  - [internal/casefile](./modules/casefile.md) → [internal/middleware](./modules/middleware.md) (imports)
  - [cmd/thlibo/compresscmd](./modules/compresscmd.md) → [internal/telemetry](./modules/telemetry.md) (imports)
  - [cmd/thlibo/execcmd](./modules/execcmd.md) → [internal/execpolicy](./modules/execpolicy.md) (imports)
  - [cmd/thlibo/execcmd](./modules/execcmd.md) → [internal/logx](./modules/logx.md) (imports)
  - [cmd/thlibo/execcmd](./modules/execcmd.md) → [internal/telemetry](./modules/telemetry.md) (imports)
  - [internal/install](./modules/install.md) → [processors](./modules/processors-1j5jcrl.md) (imports)
  - [internal/middleware](./modules/middleware.md) → [internal/telemetry](./modules/telemetry.md) (imports)
  - [internal/pdfocr](./modules/pdfocr.md) → [internal/inferd](./modules/inferd.md) (imports)
  - [internal/processors](./modules/processors-1lzwtfn.md) → [gopkg.in/yaml.v3](./references/go-gopkg-in-yaml-v3.md) (imports)
  - [cmd/thlibo/rewritecmd](./modules/rewritecmd.md) → [internal/middleware](./modules/middleware.md) (imports)
  - [cmd/thlibo/shorthandcmd](./modules/shorthandcmd.md) → [internal/config](./modules/config.md) (imports)
  - [cmd/thlibo/shorthandcmd](./modules/shorthandcmd.md) → [internal/shorthand](./modules/shorthand.md) (imports)
  - [internal/telemetry](./modules/telemetry.md) → [internal/version](./modules/version.md) (imports)
  - [cmd/thlibo](./modules/thlibo.md) → [cmd/thlibo/compresscmd](./modules/compresscmd.md) (imports)
  - [cmd/thlibo](./modules/thlibo.md) → [cmd/thlibo/configcmd](./modules/configcmd.md) (imports)
  - [cmd/thlibo](./modules/thlibo.md) → [cmd/thlibo/execcmd](./modules/execcmd.md) (imports)
  - [cmd/thlibo](./modules/thlibo.md) → [cmd/thlibo/shorthandcmd](./modules/shorthandcmd.md) (imports)
  - [AGENTS.md](./references/agents-md.md) → [cmd/thlibo/compresscmd](./modules/compresscmd.md) (documents)
  - and 31 more
- **Disconnected islands: 1.** Concepts linked to each other and to nothing else — most often documents describing code nothing connects them to.
  - 24 concepts: [CI scanners](./pipelines/ci-scanners.md), [CI secret-scanning](./pipelines/ci-secret-scanning.md), [CI test](./pipelines/ci-test.md), [Fuzz budget](./pipelines/fuzz-budget.md), [Fuzz fuzz](./pipelines/fuzz-fuzz.md), [Pages deploy](./pipelines/pages-deploy.md), [Release build](./pipelines/release-build.md), [Release release](./pipelines/release-release.md), and 16 more
- **Unconnected concepts: 33.** Nothing links to or from these: dead code, an unreferenced document, or a gap in extraction. Which of the three it is needs a human.
  - [ADR 0001: compression via pretooluse rewrite](./references/adr-0001-compression-via-pretooluse-rewrite.md)
  - [ADR 0002: one warm model single daemon](./references/adr-0002-one-warm-model-single-daemon.md)
  - [ADR 0003: per user autostart not system service](./references/adr-0003-per-user-autostart-not-system-service.md)
  - [ADR 0004: no windows shim](./references/adr-0004-no-windows-shim.md)
  - [ADR 0005: extract inference to inferd](./references/adr-0005-extract-inference-to-inferd.md)
  - [ADR 0006: fail open during inferd bootstrap](./references/adr-0006-fail-open-during-inferd-bootstrap.md)
  - [ADR 0007: pdf to markdown](./references/adr-0007-pdf-to-markdown.md)
  - [ADR 0008: numpy as processor dep](./references/adr-0008-numpy-as-processor-dep.md)
  - [ADR 0009: pdf image ocr via gemma vision](./references/adr-0009-pdf-image-ocr-via-gemma-vision.md)
  - [ADR 0010: native go processors](./references/adr-0010-native-go-processors.md)
  - [ADR 0011: optional otel emission](./references/adr-0011-optional-otel-emission.md)
  - [ADR 0012: bounded inference calls](./references/adr-0012-bounded-inference-calls.md)
  - [ADR 0013: router candidate eligibility](./references/adr-0013-router-candidate-eligibility.md)
  - [ADR 0014: fast path match precedence](./references/adr-0014-fast-path-match-precedence.md)
  - [ADR 0015: native go pdf extraction](./references/adr-0015-native-go-pdf-extraction.md)
  - [ADR 0016: native go cordon filter](./references/adr-0016-native-go-cordon-filter.md)
  - [github.com/cenkalti/backoff/v5](./references/go-github-com-cenkalti-backoff-v5.md)
  - [github.com/cespare/xxhash/v2](./references/go-github-com-cespare-xxhash-v2.md)
  - [github.com/go-logr/logr](./references/go-github-com-go-logr-logr.md)
  - [github.com/go-logr/stdr](./references/go-github-com-go-logr-stdr.md)
  - and 13 more
- **Merge gates: 6 of 11 CI jobs.** These run on a pull request or on a push to the default branch, so they are the automated checks a change meets. Which of them is *required* is configured on the repository and is not in the tree.
  - [CI scanners](./pipelines/ci-scanners.md)
  - [CI secret-scanning](./pipelines/ci-secret-scanning.md)
  - [CI test](./pipelines/ci-test.md)
  - [Pages deploy](./pipelines/pages-deploy.md)
  - [signpost the bundle is current on main](./pipelines/signpost-the-bundle-is-current-on-main.md)
  - [signpost the bundle still describes this tree](./pipelines/signpost-the-bundle-still-describes-this-tree.md)

### Modules

- [processors/cargo-filter](./modules/cargo-filter.md) — 1 python file; 11 exported symbols; entrypoint __main__; package processors.cargo-filter.run.
- [cmd/thlibo/casecmd](./modules/casecmd.md) — 3 go files; 8 exported symbols.
- [internal/casefile](./modules/casefile.md) — 2 go files; 9 exported symbols.
- [internal/adapters/claudecode](./modules/claudecode.md) — 12 go files; 26 exported symbols; entrypoint #!.
- [internal/adapters/codex](./modules/codex.md) — 8 go files; 18 exported symbols; entrypoint #!.
- [cmd/thlibo/compresscmd](./modules/compresscmd.md) — 2 go files; 2 exported symbols.
- [internal/config](./modules/config.md) — 2 go files; 5 exported symbols.
- [cmd/thlibo/configcmd](./modules/configcmd.md) — 3 go files; 6 exported symbols.
- [internal/adapters/copilot](./modules/copilot.md) — 7 go files; 13 exported symbols; entrypoint #!.
- [processors/cordon-filter](./modules/cordon-filter.md) — 1 python file; 12 exported symbols; entrypoint __main__; package processors.cordon-filter.run.
- [internal/adapters/cursor](./modules/cursor.md) — 5 go files; 7 exported symbols; entrypoint #!.
- [cmd/thlibo/execcmd](./modules/execcmd.md) — 4 go files; 5 exported symbols.
- [internal/execpolicy](./modules/execpolicy.md) — 2 go files; 8 exported symbols.
- [processors/git-filter](./modules/git-filter.md) — 1 python file; 2 exported symbols; entrypoint __main__; package processors.git-filter.run.
- [processors/go-test-filter](./modules/go-test-filter.md) — 1 python file; 16 exported symbols; entrypoint __main__; package processors.go-test-filter.run.
- [internal/adapters/hookpath](./modules/hookpath.md) — 1 go file; 1 exported symbol.
- [internal/inferd](./modules/inferd.md) — 9 go files; 29 exported symbols.
- [internal/install](./modules/install.md) — 22 go files; 17 exported symbols.
- [cmd/thlibo/installcmd](./modules/installcmd.md) — 2 go files; 1 exported symbol.
- [processors/lint-filter](./modules/lint-filter.md) — 1 python file; 4 exported symbols; entrypoint __main__; package processors.lint-filter.run.
- [internal/logx](./modules/logx.md) — 2 go files; 22 exported symbols.
- [internal/middleware](./modules/middleware.md) — 13 go files; 9 exported symbols.
- [processors/ndjson-filter](./modules/ndjson-filter.md) — 1 python file; 3 exported symbols; entrypoint __main__; package processors.ndjson-filter.run.
- [processors/npm-filter](./modules/npm-filter.md) — 1 python file; 7 exported symbols; entrypoint __main__; package processors.npm-filter.run.
- [internal/pdf](./modules/pdf.md) — 29 go files; 111 exported symbols.
- [processors/pdf-to-md](./modules/pdf-to-md.md) — 1 python file; 11 exported symbols; entrypoint __main__; package processors.pdf-to-md.run.
- [internal/pdfocr](./modules/pdfocr.md) — 3 go files; 3 exported symbols.
- [processors](./modules/processors-1j5jcrl.md) — 1 go file; 1 exported symbol; package builtins.
- [internal/processors](./modules/processors-1lzwtfn.md) — 46 go files; 49 exported symbols; entrypoint init.
- [internal/promptsan](./modules/promptsan.md) — 2 go files; 2 exported symbols.
- [processors/pytest-filter](./modules/pytest-filter.md) — 1 python file; 11 exported symbols; entrypoint __main__; package processors.pytest-filter.run.
- [cmd/thlibo/rewritecmd](./modules/rewritecmd.md) — 2 go files; 6 exported symbols.
- [internal/router](./modules/router.md) — 2 go files; 7 exported symbols.
- [scripts](./modules/scripts.md) — 3 powershell files; 10 exported symbols; entrypoint #!, __main__; package scripts.run_processor_tests.
- [internal/shellcmd](./modules/shellcmd.md) — 2 go files; 2 exported symbols.
- [internal/shorthand](./modules/shorthand.md) — 4 go files; 10 exported symbols.
- [cmd/thlibo/shorthandcmd](./modules/shorthandcmd.md) — 5 go files; 8 exported symbols.
- [processors/stacktrace-filter](./modules/stacktrace-filter.md) — 1 python file; 23 exported symbols; entrypoint __main__; package processors.stacktrace-filter.run.
- [internal/telemetry](./modules/telemetry.md) — 4 go files; 22 exported symbols.
- [cmd/thlibo](./modules/thlibo.md) — 2 go files; entrypoint main; package main.
- [processors/trivy-filter](./modules/trivy-filter.md) — 1 python file; 3 exported symbols; entrypoint __main__; package processors.trivy-filter.run.
- [cmd/thlibo/uninstallcmd](./modules/uninstallcmd.md) — 2 go files; 4 exported symbols.
- [internal/update](./modules/update.md) — 12 go files; 15 exported symbols.
- [cmd/thlibo/upgradecmd](./modules/upgradecmd.md) — 2 go files; 1 exported symbol.
- [internal/version](./modules/version.md) — 2 go files; 2 exported symbols.

### Pipelines

- [CI scanners](./pipelines/ci-scanners.md) — CI job scanners in the CI workflow, 12 steps; runs on a pull request or a default-branch push
- [CI secret-scanning](./pipelines/ci-secret-scanning.md) — CI job secret-scanning in the CI workflow, 2 steps; runs on a pull request or a default-branch push
- [CI test](./pipelines/ci-test.md) — CI job test (${{ matrix.os }}, Go ${{ matrix.go }}) in the CI workflow, 7 steps; runs on a pull request or a default-branch push
- [Fuzz budget](./pipelines/fuzz-budget.md) — CI job budget in the Fuzz workflow, 1 step
- [Fuzz fuzz](./pipelines/fuzz-fuzz.md) — CI job fuzz ${{ matrix.target }} in the Fuzz workflow, 5 steps
- [Pages deploy](./pipelines/pages-deploy.md) — CI job deploy in the Pages workflow, 4 steps; runs on a pull request or a default-branch push
- [Release build](./pipelines/release-build.md) — CI job build (${{ matrix.goos }}-${{ matrix.goarch }}) in the Release workflow, 7 steps
- [Release release](./pipelines/release-release.md) — CI job release in the Release workflow, 7 steps
- [Release verify-install](./pipelines/release-verify-install.md) — CI job verify-install (${{ matrix.os }}) in the Release workflow, 4 steps
- [signpost the bundle is current on main](./pipelines/signpost-the-bundle-is-current-on-main.md) — CI job the bundle is current on main in the signpost workflow, 3 steps; runs on a pull request or a default-branch push
- [signpost the bundle still describes this tree](./pipelines/signpost-the-bundle-still-describes-this-tree.md) — CI job the bundle still describes this tree in the signpost workflow, 3 steps; runs on a pull request or a default-branch push

### Documents

- [ADR 0001: compression via pretooluse rewrite](./references/adr-0001-compression-via-pretooluse-rewrite.md) — Architecture decision (Accepted), 20 rules read from 0001-compression-via-pretooluse-rewrite.md.
- [ADR 0002: one warm model single daemon](./references/adr-0002-one-warm-model-single-daemon.md) — Architecture decision (Superseded), 19 rules read from 0002-one-warm-model-single-daemon.md.
- [ADR 0003: per user autostart not system service](./references/adr-0003-per-user-autostart-not-system-service.md) — Architecture decision (Accepted), 22 rules read from 0003-per-user-autostart-not-system-service.md.
- [ADR 0004: no windows shim](./references/adr-0004-no-windows-shim.md) — Architecture decision (Accepted), 21 rules read from 0004-no-windows-shim.md.
- [ADR 0005: extract inference to inferd](./references/adr-0005-extract-inference-to-inferd.md) — Architecture decision (Accepted), 32 rules read from 0005-extract-inference-to-inferd.md.
- [ADR 0006: fail open during inferd bootstrap](./references/adr-0006-fail-open-during-inferd-bootstrap.md) — Architecture decision (Accepted), 33 rules read from 0006-fail-open-during-inferd-bootstrap.md.
- [ADR 0007: pdf to markdown](./references/adr-0007-pdf-to-markdown.md) — Architecture decision (Accepted), 53 rules read from 0007-pdf-to-markdown.md.
- [ADR 0008: numpy as processor dep](./references/adr-0008-numpy-as-processor-dep.md) — Architecture decision (Accepted), 36 rules read from 0008-numpy-as-processor-dep.md.
- [ADR 0009: pdf image ocr via gemma vision](./references/adr-0009-pdf-image-ocr-via-gemma-vision.md) — Architecture decision (Accepted), 36 rules read from 0009-pdf-image-ocr-via-gemma-vision.md.
- [ADR 0010: native go processors](./references/adr-0010-native-go-processors.md) — Architecture decision (Accepted), 27 rules read from 0010-native-go-processors.md.
- [ADR 0011: optional otel emission](./references/adr-0011-optional-otel-emission.md) — Architecture decision (Accepted), 45 rules read from 0011-optional-otel-emission.md.
- [ADR 0012: bounded inference calls](./references/adr-0012-bounded-inference-calls.md) — Architecture decision (Accepted), 23 rules read from 0012-bounded-inference-calls.md.
- [ADR 0013: router candidate eligibility](./references/adr-0013-router-candidate-eligibility.md) — Architecture decision (Accepted), 25 rules read from 0013-router-candidate-eligibility.md.
- [ADR 0014: fast path match precedence](./references/adr-0014-fast-path-match-precedence.md) — Architecture decision (Accepted), 34 rules read from 0014-fast-path-match-precedence.md.
- [ADR 0015: native go pdf extraction](./references/adr-0015-native-go-pdf-extraction.md) — Architecture decision (Accepted), 47 rules read from 0015-native-go-pdf-extraction.md.
- [ADR 0016: native go cordon filter](./references/adr-0016-native-go-cordon-filter.md) — Architecture decision (Accepted), 36 rules read from 0016-native-go-cordon-filter.md.
- [AGENTS.md](./references/agents-md.md) — Stated constraints, 9 rules read from AGENTS.md.
- [CLAUDE.md](./references/claude-md.md) — Stated constraints, 58 rules read from CLAUDE.md.
- [README.md](./references/readme-md.md) — Architecture decision, 7 rules read from README.md.

### External dependencies

- [actions/cache](./references/github-actions-actions-cache.md) — github-actions dependency actions/cache (55cc8345863c7cc4c66a329aec7e433d2d1c52a9)
- [actions/checkout](./references/github-actions-actions-checkout.md) — github-actions dependency actions/checkout (3d3c42e5aac5ba805825da76410c181273ba90b1)
- [actions/configure-pages](./references/github-actions-actions-configure-pages.md) — github-actions dependency actions/configure-pages (45bfe0192ca1faeb007ade9deae92b16b8254a0d)
- [actions/deploy-pages](./references/github-actions-actions-deploy-pages.md) — github-actions dependency actions/deploy-pages (368f82528645a54fb793d4d04e342629a3f51346)
- [actions/download-artifact](./references/github-actions-actions-download-artifact.md) — github-actions dependency actions/download-artifact (3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c)
- [actions/setup-go](./references/github-actions-actions-setup-go.md) — github-actions dependency actions/setup-go (b7ad1dad31e06c5925ef5d2fc7ad053ef454303e)
- [actions/setup-python](./references/github-actions-actions-setup-python.md) — github-actions dependency actions/setup-python (5fda3b95a4ea91299a34e894583c3862153e4b97)
- [actions/upload-artifact](./references/github-actions-actions-upload-artifact.md) — github-actions dependency actions/upload-artifact (043fb46d1a93c77aae656e7c1c64a875d1fc6a0a)
- [actions/upload-pages-artifact](./references/github-actions-actions-upload-pages-artifact.md) — github-actions dependency actions/upload-pages-artifact (fc324d3547104276b827a68afc52ff2a11cc49c9)
- [anchore/sbom-action](./references/github-actions-anchore-sbom-action.md) — github-actions dependency anchore/sbom-action (3ad7283483fc7af8ff2b4ea19663c2d5ca935e26)
- [gitleaks/gitleaks-action](./references/github-actions-gitleaks-gitleaks-action.md) — github-actions dependency gitleaks/gitleaks-action (e0c47f4f8be36e29cdc102c57e68cb5cbf0e8d1e)
- [sigstore/cosign-installer](./references/github-actions-sigstore-cosign-installer.md) — github-actions dependency sigstore/cosign-installer (6f9f17788090df1f26f669e9d70d6ae9567deba6)
- [softprops/action-gh-release](./references/github-actions-softprops-action-gh-release.md) — github-actions dependency softprops/action-gh-release (efb35369e0ad2afab669f228072c1b0d510eae64)
- [github.com/cenkalti/backoff/v5](./references/go-github-com-cenkalti-backoff-v5.md) — go dependency github.com/cenkalti/backoff/v5 (v5.0.3)
- [github.com/cespare/xxhash/v2](./references/go-github-com-cespare-xxhash-v2.md) — go dependency github.com/cespare/xxhash/v2 (v2.3.0)
- [github.com/go-logr/logr](./references/go-github-com-go-logr-logr.md) — go dependency github.com/go-logr/logr (v1.4.4)
- [github.com/go-logr/stdr](./references/go-github-com-go-logr-stdr.md) — go dependency github.com/go-logr/stdr (v1.2.2)
- [github.com/google/uuid](./references/go-github-com-google-uuid.md) — go dependency github.com/google/uuid (v1.6.0)
- [github.com/grpc-ecosystem/grpc-gateway/v2](./references/go-github-com-grpc-ecosystem-grpc-gateway-v2.md) — go dependency github.com/grpc-ecosystem/grpc-gateway/v2 (v2.31.0)
- [github.com/kr/text](./references/go-github-com-kr-text.md) — go dependency github.com/kr/text (v0.2.0)
- [go.opentelemetry.io/auto/sdk](./references/go-go-opentelemetry-io-auto-sdk.md) — go dependency go.opentelemetry.io/auto/sdk (v1.2.1)
- [go.opentelemetry.io/otel](./references/go-go-opentelemetry-io-otel.md) — go dependency go.opentelemetry.io/otel (v1.47.0)
- [go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc](./references/go-go-opentelemetry-io-otel-exporters-otlp-otlplog-otlploggrpc.md) — go dependency go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc (v0.23.0)
- [go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp](./references/go-go-opentelemetry-io-otel-exporters-otlp-otlplog-otlploghttp.md) — go dependency go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp (v0.23.0)
- [go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc](./references/go-go-opentelemetry-io-otel-exporters-otlp-otlpmetric-otlpmetricgrpc.md) — go dependency go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc (v1.47.0)
- [go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp](./references/go-go-opentelemetry-io-otel-exporters-otlp-otlpmetric-otlpmetrichttp.md) — go dependency go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp (v1.47.0)
- [go.opentelemetry.io/otel/exporters/stdout/stdoutlog](./references/go-go-opentelemetry-io-otel-exporters-stdout-stdoutlog.md) — go dependency go.opentelemetry.io/otel/exporters/stdout/stdoutlog (v0.23.0)
- [go.opentelemetry.io/otel/exporters/stdout/stdoutmetric](./references/go-go-opentelemetry-io-otel-exporters-stdout-stdoutmetric.md) — go dependency go.opentelemetry.io/otel/exporters/stdout/stdoutmetric (v1.47.0)
- [go.opentelemetry.io/otel/log](./references/go-go-opentelemetry-io-otel-log.md) — go dependency go.opentelemetry.io/otel/log (v1.47.0)
- [go.opentelemetry.io/otel/metric](./references/go-go-opentelemetry-io-otel-metric.md) — go dependency go.opentelemetry.io/otel/metric (v1.47.0)
- [go.opentelemetry.io/otel/sdk](./references/go-go-opentelemetry-io-otel-sdk.md) — go dependency go.opentelemetry.io/otel/sdk (v1.47.0)
- [go.opentelemetry.io/otel/sdk/log](./references/go-go-opentelemetry-io-otel-sdk-log.md) — go dependency go.opentelemetry.io/otel/sdk/log (v1.47.0)
- [go.opentelemetry.io/otel/sdk/metric](./references/go-go-opentelemetry-io-otel-sdk-metric.md) — go dependency go.opentelemetry.io/otel/sdk/metric (v1.47.0)
- [go.opentelemetry.io/otel/trace](./references/go-go-opentelemetry-io-otel-trace.md) — go dependency go.opentelemetry.io/otel/trace (v1.47.0)
- [go.opentelemetry.io/proto/otlp](./references/go-go-opentelemetry-io-proto-otlp.md) — go dependency go.opentelemetry.io/proto/otlp (v1.11.1)
- [golang.org/x/image](./references/go-golang-org-x-image.md) — go dependency golang.org/x/image (v0.45.0)
- [golang.org/x/net](./references/go-golang-org-x-net.md) — go dependency golang.org/x/net (v0.59.0)
- [golang.org/x/sys](./references/go-golang-org-x-sys.md) — go dependency golang.org/x/sys (v0.48.0)
- [golang.org/x/term](./references/go-golang-org-x-term.md) — go dependency golang.org/x/term (v0.46.0)
- [golang.org/x/text](./references/go-golang-org-x-text.md) — go dependency golang.org/x/text (v0.42.0)
- [google.golang.org/genproto/googleapis/api](./references/go-google-golang-org-genproto-googleapis-api.md) — go dependency google.golang.org/genproto/googleapis/api (v0.0.0-20260928230214-8a89bd6388cc)
- [google.golang.org/genproto/googleapis/rpc](./references/go-google-golang-org-genproto-googleapis-rpc.md) — go dependency google.golang.org/genproto/googleapis/rpc (v0.0.0-20260928230214-8a89bd6388cc)
- [google.golang.org/grpc](./references/go-google-golang-org-grpc.md) — go dependency google.golang.org/grpc (v1.84.0)
- [google.golang.org/protobuf](./references/go-google-golang-org-protobuf.md) — go dependency google.golang.org/protobuf (v1.36.12)
- [gopkg.in/yaml.v3](./references/go-gopkg-in-yaml-v3.md) — go dependency gopkg.in/yaml.v3 (v3.0.1)
- [pdfplumber](./references/pypi-pdfplumber.md) — pypi dependency pdfplumber (>=0.11)
- [pypdf](./references/pypi-pypdf.md) — pypi dependency pypdf (>=4.0)
<!-- /signpost:managed:index -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
