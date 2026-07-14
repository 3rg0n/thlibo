# AGENTS.md

The canonical agent guidance for this repo lives in
[`CLAUDE.md`](./CLAUDE.md) — architecture, invariants, processor model,
inferd wire protocol, adapters, and the build/test/scan commands.

This file exists so agents that look for `AGENTS.md` by convention
(Codex, Cursor, GitHub Copilot, and others) find the pointer. thlibo
itself ships hooks for all of them; **read `CLAUDE.md` first.**

Fast facts (see `CLAUDE.md` for the authoritative version):

- **Build / test / scan:** `go build ./...` · `go test ./...` ·
  `go vet ./...` · `staticcheck ./...` · `gosec ./cmd/... ./internal/...
  ./processors/...` — all four gate CI.
- **Cardinal rule:** fail open. On any error path the middleware
  returns the original bytes; it must never break the AI client
  (ADR 0006).
- **Authoritative sources when they disagree:** `THREAT_MODEL.md`
  (security), `docs/adr/*.md` (architecture), then `CLAUDE.md`.
- **`main` is branch-protected:** land changes via PR with green CI
  (test on linux/macOS/Windows + scanners + secret-scanning).
