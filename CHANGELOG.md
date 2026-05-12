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
