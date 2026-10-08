---
type: Practices
title: How work is done here
description: "What this repository declares about building, testing, gating, and ownership — and what it does not."
---
# How work is done here

Each line is something this repository states, or something it does not. A missing declaration is not a criticism and there is no score here: it is a fact about what an agent can rely on, and the absences are the ones worth reading, because they are what it would otherwise have to guess.
<!-- signpost:managed:practices -->
### Building

- **Not declared.** No build command is declared. An agent asked to build this repository has to infer how, and its first guess is not reviewable.
  - Looked in Makefile targets, package.json scripts, Cargo aliases, CMake targets, Bazel targets.

### Testing

- **Not declared.** No test command is declared. This is the fact an agent most needs before it offers to add a test, because it decides where the test goes and how it is run.
  - Looked in Makefile targets, package.json scripts, Cargo aliases, CMake targets, Bazel targets.
- 102 test files in the tree.

### What runs against a change

- 6 jobs run on a pull request or on a push to the default branch: `deploy`, `scanners`, `secret-scanning`, `test (${{ matrix.os }}, Go ${{ matrix.go }})`, `the bundle is current on main`, `the bundle still describes this tree`. Which of them is *required* is configured on the repository and is not in the tree.
  - Stated in `.github/workflows/ci.yml` line 29, `.github/workflows/pages.yml` line 26, and `.github/workflows/signpost.yml` line 41.
- 5 further CI jobs run outside that gate — on a schedule, a tag, or manually.
  - Stated in `.github/workflows/fuzz.yml` line 79 and `.github/workflows/release.yml` line 26.

### Dependencies

- The Go dependencies are pinned by a lockfile.
  - Stated in `go.sum`.
- **Not declared.** The Python dependencies are declared but not pinned by any lockfile in the tree, so two builds can resolve different versions.
  - Looked in `processors/pdf-to-md/requirements.txt`.
- Automated dependency updates are configured.
  - Stated in `.github/dependabot.yml`.

### Ownership and policy

- **Not declared.** No CODEOWNERS rules were found, so nothing states who reviews a change to a given path.
  - Looked in `CODEOWNERS`, `.github/CODEOWNERS`, and `docs/CODEOWNERS`.
- The repository states its licence.
  - Stated in `LICENSE`.
- **Not declared.** No security policy was found, so someone who finds a vulnerability has to guess where to send it.
  - Looked in `SECURITY.md`, `.github/SECURITY.md`, and `docs/SECURITY.md`.

### Documentation

- The repository has a README.
  - Stated in `README.md`.
- 34 documentation files in the tree, outside the bundle.

### Observability

- An observability library is a declared dependency: `go.opentelemetry.io/auto/sdk`, `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc`, `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp`, `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc`, `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp`, and 9 others. Whether anything is instrumented with it is not something a manifest can say.
  - Stated in `go.mod` line 13.

### Instructions for agents

- 67 stated rules for agents working in this repository.
  - Stated in `AGENTS.md` and `CLAUDE.md`.
- 17 architecture decision records state why things are the way they are.
  - Stated in `docs/adr/0001-compression-via-pretooluse-rewrite.md`, `docs/adr/0002-one-warm-model-single-daemon.md`, `docs/adr/0003-per-user-autostart-not-system-service.md`, `docs/adr/0004-no-windows-shim.md`, `docs/adr/0005-extract-inference-to-inferd.md`, `docs/adr/0006-fail-open-during-inferd-bootstrap.md`, and 11 other files.
<!-- /signpost:managed:practices -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
