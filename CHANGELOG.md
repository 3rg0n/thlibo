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

### Changed

- Spec amended: request/response frames now carry a client-generated `id` field, echoed on every response frame. Admin status frames use `id: "admin"`.
