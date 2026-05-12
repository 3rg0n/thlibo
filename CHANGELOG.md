# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial repo scaffold: `cmd/thlibod`, `cmd/thlibo`, `internal/{daemon,ipc,processors,router,queue,adapters/{claudecode,codex,proxy},install}`, and `processors/` for built-ins.
- `go.mod` at module path `github.com/3rg0n/thlibo`, pinned to Go 1.22.
- `.gitignore` covering Go build artifacts, GGUF model files, secrets, IDE files, and test sandboxes.
- `.plan/thlibo-spec.md` — v0.1 spec (source of truth).
- `.plan/release-gate.md` — mechanical release gate, one row per spec requirement.
- `CLAUDE.md` — guidance for future Claude Code sessions.
- **Phase 1 — daemon spine.** Newline-delimited JSON protocol (ipc), Gemma 4 sampling defaults, image-token-budget validation (A5/A6/A7). Single-instance `flock`/`LockFileEx` lock (A2). Platform-specific IPC endpoints: Unix sockets with group+mode, Windows named pipes via `go-winio` with SDDL granting current-user only, TCP loopback fallback (A3). `SubprocessEngine` abstraction + `llamafile-stub` test binary (A1). Daemon lifecycle: ready-gating, delayed socket creation, admin status frames, graceful shutdown (A4/A10/A12). 28 tests total, all scanners clean.
- **Phase 2 — daemon robustness.** `internal/queue` admission layer: 1 active job, 10 queued default, non-blocking `Submit` with `ErrFull` on overflow, per-job context for cancellation (A8). Real queue-backed inference dispatch in the daemon; client disconnect cancels the in-flight job via ctx cancellation plumbed through `SubprocessEngine.Generate` (A9). Engine supervisor with 3-attempt lifetime restart cap; admin clients receive status broadcasts (`restarting_engine_attempt_N`, `ready`) and a terminal error on exhaustion (A11). 35 tests total, all scanners clean.
- **Phase 3 — middleware core.** `internal/processors` registry loads YAML + markdown descriptors from user + builtin sources, user wins on conflict (B2, B3, C5). Strict YAML decoder rejects unknown fields; broken descriptors are quarantined with a warning instead of failing the whole scan (B8g). `internal/router` builds the daemon routing prompt dynamically, constrains output with a GBNF grammar analogous to Anthropic API `input_schema`, and parses responses defensively (B5). `internal/middleware` wires the main flow: 2000-byte short-circuit (B1), fast-path regex (B4), router call (B5), `none` passthrough (B6), chaining (B7). 8-row fallback matrix as a single table-driven test covers B8a through B8h. Script dispatcher with configurable `ScriptTimeout` (B8e). 77 tests total, all scanners clean. Spec amended: `grammar` field added to request envelope.
- **Post-Phase-3 refinements from reading the Gemma 4 model card.** Router refactored to use Gemma's native tool-call format (`<|tool_call>call:route{processors:[...]}<tool_call|>`) instead of freeform JSON, with a GBNF grammar targeting the trained-for token pattern. Mandatory thought-stripping (`processors.Strip`) applied to every prompt-processor response — Gemma E2B/E4B emits a `<|channel>thought...<channel|>` block even with thinking disabled, and leaving it in would leak model internals to the AI client (C7). Spec amended to document both.
- **Phase 4 — built-in processors.** 5 processors embedded via `go:embed` at `processors/{git-filter,npm-filter,cargo-filter,compress,casefolder}/`: 3 Python script filters, 2 Gemma-aligned prompt processors (casefolder uses `<|think|>` for reasoning). `middleware.BuildRegistry` merges the embedded FS with an optional `~/.thlibo/processors/` user source; missing user dir is silently skipped, same-named user processor overrides the built-in (C4, C5). Script C6 test mirrors the embed.FS to disk and runs each filter against a representative fixture — all 3 produce strictly shorter output than input. 86 tests total, all scanners clean.

### Changed

- Spec amended: request/response frames now carry a client-generated `id` field, echoed on every response frame. Admin status frames use `id: "admin"`.
