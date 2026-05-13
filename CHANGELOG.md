# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-05-13

First release. A working local-Gemma compression middleware for
Claude Code + Codex CLI.

### Added

#### Daemon (`thlibod`)

- Newline-delimited JSON protocol with per-request `id` correlation,
  Gemma 4 sampling defaults (temperature 1.0, top_p 0.95, top_k 64),
  image-token-budget validation, `grammar` field for GBNF output
  constraints.
- Single-instance lock (`flock` on Unix, `LockFileEx` on Windows).
- Platform-specific IPC: Unix domain sockets with group + mode,
  Windows named pipes via `go-winio` with SDDL granting current-user
  only, TCP loopback fallback.
- Engine-agnostic `SubprocessEngine` abstraction + an in-repo
  `llamafile-stub` for tests. Ready-gated socket creation, graceful
  drain-and-exit on SIGTERM.
- Admission queue: 1 active generation, 10 queued by default,
  non-blocking `Submit` with `ErrFull` on overflow. Client disconnect
  cancels the in-flight job via context propagation.
- Engine supervisor: up to 3 lifetime restart attempts on llamafile
  crash; admin clients receive `restarting_engine_attempt_N` /
  `ready` status broadcasts and a terminal error on exhaustion.

#### Middleware (`thlibo`)

- Processor registry: YAML + markdown descriptors from embedded
  built-ins and `~/.thlibo/processors/`. User entries override
  built-ins by name. Strict YAML decoder rejects unknown fields;
  broken descriptors are quarantined with a warning instead of
  aborting the scan.
- Pipeline: 2000-byte short-circuit → fast-path regex match → daemon
  routing call → processor chain → compressed output. Every failure
  mode falls back to the original bytes (8-case fallback matrix).
- Router uses Gemma 4's native tool-call format
  (`<|tool_call>call:route{processors:[...]}<tool_call|>`) with a
  GBNF grammar that enforces the trained-for token pattern
  token-by-token.
- Mandatory thought-stripping: `processors.Strip` removes the
  `<|channel>thought…<channel|>` block Gemma emits before every
  answer (including the empty block when thinking is disabled),
  so model internals don't leak into the AI client's context.

#### Built-in processors

- Five embedded processors shipped via `go:embed` under
  `processors/`:
  - `git-filter` (script, Python) — `git status`/`diff`/`log`
  - `npm-filter` (script, Python) — `npm`/`npx`/`pnpm`/`yarn`
  - `cargo-filter` (script, Python) — `cargo build`/`test`/`clippy`
  - `compress` (prompt) — generic verbose-output summariser
  - `casefolder` (prompt, thinking-enabled) — stack traces, error
    logs, crash output

#### Client adapters

- **Claude Code** (`internal/adapters/claudecode`): PreToolUse hook
  that calls `thlibo rewrite` and emits `updatedInput` so the Bash
  tool runs a `thlibo exec -- <cmd>` wrapper instead of the raw
  command. `MergeSettings` is idempotent, preserves every unrelated
  key, refuses to overwrite malformed JSON, normalises Windows paths
  to forward slashes so `bash -c` doesn't eat the backslashes.
- **Codex CLI** (`internal/adapters/codex`): PostToolUse hook that
  replaces `tool_response` with a compressed version via
  `decision:block` + `reason`. Installer also enables
  `[features] codex_hooks = true` in `config.toml` (required or
  Codex silently ignores hooks) and merges `hooks.json`.

#### CLI

- `thlibo rewrite <cmd>` — registry lookup keyed on argv[0],
  exit-code protocol (0=rewrite / 1=passthrough / 2=deny reserved /
  3=ask reserved). Emits an absolute-path
  `thlibo exec --` prefix so the rewritten command runs under
  Claude Code's Bash tool without PATH inheritance.
- `thlibo exec -- <cmd>` — subprocess wrapper. Runs the command,
  captures stdout, pipes through `middleware.Process`, emits
  compressed stdout with stderr + exit code preserved verbatim.
- `thlibo compress` — read stdin, compress, write stdout. Used by
  the Codex hook and for shell pipelines.
- `thlibo install` — mirrors built-ins to disk, writes + merges the
  Claude Code hook, registers `thlibod` for per-user autostart
  (Windows Startup folder / macOS LaunchAgent / Linux systemd user
  unit). Optional `--codex`, `--pull-model`, `--allow-unpinned`,
  `--dry-run`, `--engine-path`.
- `thlibo pull [name]` — HTTPS-only GGUF downloader with HTTP Range
  resume, SHA-256 verification, progress indicator, context
  cancellation. Tests never hit the real network (httptest.Server).

#### Infrastructure

- GitHub Actions `ci.yml`: matrix build+test on ubuntu/macos/windows
  with Go 1.22; scanner job runs `staticcheck`, `govulncheck`,
  `gosec`, `semgrep --config=auto`; secrets job runs `gitleaks`.
- GitHub Actions `release.yml`: tagged-release workflow downloads
  the pinned GGUF once, computes its SHA-256, builds 4 platform
  bundles (linux-amd64/arm64, darwin-arm64, windows-amd64) with
  `-ldflags -X ...pinnedGemma4E4BQ4KM=<sha>`, attaches the GGUF as
  a release asset, publishes a draft release with SHA256SUMS.
- `DefaultModel.ExpectedSHA256` pinned to
  `51865750adafd22de56994a343d5a887cc1a589b9bae41d62b748c8bd0ca9c76`
  for `bartowski/google_gemma-4-E4B-it-GGUF/google_gemma-4-E4B-it-Q4_K_M.gguf`
  (5.4 GB). CI builds can override per-release via `-ldflags -X`.
- Token-savings measurements recorded in
  [.plan/release-notes-0.1.0.md](.plan/release-notes-0.1.0.md):
  97.6% on git diff, 99.4% on npm list, 89.2% on cargo test, 5.4%
  on git status.
- README with install/uninstall/customize/disable/security/limitations
  sections.
- 184 tests across the project. `staticcheck`, `govulncheck`,
  `gosec`, `gitleaks`, `semgrep` clean on shipped code.

### Changed

- Spec: request/response frames now carry a client-generated `id`
  field, echoed on every response. Admin status frames use
  `id: "admin"`.
- Spec: request envelope gained a `grammar` field for GBNF output
  constraints.
- Spec URL for the canonical GGUF corrected to
  `bartowski/google_gemma-4-E4B-it-GGUF` (earlier placeholder path
  did not resolve).

### Deferred

- **D3 — proxy mode (`ANTHROPIC_BASE_URL=...`).** Would cover
  `Read`/`Grep`/`Glob` and MCP tools that bypass the Bash-rewrite
  path. Every example in the spec's own token-savings table is
  Bash-produced, so v0.1 ships without it. v0.2 candidate.

### Not needed

- **E1 — shared `thlibo-users` group.** Per-user autostart model
  has the daemon running as the invoking user, with an IPC ACL
  already scoped to the current user's SID on Windows. No shared
  group required. Gate row kept struck-through as a deliberate
  decision, not an oversight.
