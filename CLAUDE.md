# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

**Pre-implementation.** The only file in the repo is `.plan/thlibo-spec.md` (the v0.1 spec). No source, build, test, or lint commands exist yet. When in doubt about intent, the spec is authoritative — read it before making design decisions.

Planned language/tooling per the spec's repo structure (`cmd/`, `internal/`): Go. There is no `go.mod` yet.

## What this project is

Thlibo is a two-binary system that compresses AI-coding-assistant tool output using a locally-hosted Gemma 4 E4B model:

- **`thlibod`** — inference daemon. Launched by `launchd` / `systemd`. Spawns `llamafile` as a private child, loads the model once, stays warm, serves newline-delimited JSON over a Unix socket (or named pipe on Windows). Knows only about inference.
- **`thlibo`** — middleware. Invoked by AI-client hooks (Claude Code `PostToolUse`, Codex equivalent, or a proxy mode). Scans `~/.thlibo/processors/`, routes tool output to the right processor (script or prompt), posts fully-formed requests to the daemon, returns the compressed result. Knows only about routing.

## Architectural invariants

These are load-bearing — do not blur them when adding code:

1. **Daemon has zero knowledge of processors, hooks, routing, or clients.** It only accepts fully-formed `messages` arrays + sampling params and streams tokens back. The middleware builds every prompt.
2. **Middleware has zero knowledge of llamafile, model loading, or inference mechanics.** It only speaks the daemon's JSON protocol.
3. **Fallback to original output on any error path.** The middleware must never break the AI client. Non-zero script exit, daemon unreachable, parse failure, timeout → pass through the original bytes.
4. **Short-circuit before doing any work.** Input under 2000 chars passes through without scanning processors or calling the daemon.
5. **Fast-path before routing.** Each processor's `match` regex is checked before the daemon is asked to route — a regex hit dispatches immediately with no routing call.
6. **Single daemon instance.** Enforced by lock file (`/run/thlibo/thlibod.lock`). The daemon never restarts the model on its own; only `llamafile` crashes trigger restart (max 3 attempts).
7. **Concurrency is fixed: 1 active generation, 10 queued, queue-full errors immediately, client disconnect cancels the job.** Don't add backpressure schemes beyond this.
8. **Thinking mode is owned by the processor prompt, not the daemon.** The daemon has no `thinking` toggle; the `<|think|>` token lives in the processor's `processor.md` body and the GGUF chat template handles it.

## Processors

Processors live in `~/.thlibo/processors/<name>/`. Two kinds, distinguished by descriptor file:

- `processor.yaml` present → **script processor**. `entry` field names the executable (`.py` → `python3`, `.sh` → `bash`, `.exe`/`.bin` → direct exec). stdin in, stdout out, non-zero exit = fallback.
- `processor.md` present → **prompt processor**. YAML frontmatter is config (`temperature`, `max_tokens`, `match`, `thinking`, etc.); the markdown body is the system prompt sent to the daemon verbatim.
- Both present → yaml wins for type, md body becomes the system prompt.
- Neither present → folder is ignored.

Built-in processors (`compress`, `casefolder`, `git-filter`, `npm-filter`, `cargo-filter`) are embedded into the `thlibo` binary at build time; a user processor of the same name in `~/.thlibo/processors/` overrides the built-in.

## IPC

| Platform | Inference endpoint | Admin endpoint |
|---|---|---|
| Linux    | `/run/thlibo/infer.sock`     | `/run/thlibo/admin.sock` |
| macOS    | `/var/run/thlibo/infer.sock` | `/var/run/thlibo/admin.sock` |
| Windows  | `\\.\pipe\thlibo-infer`      | `\\.\pipe\thlibo-admin` |
| Fallback | `127.0.0.1:47320` (loopback) | — |

Permissions: infer socket `0660`, group `thlibo-users`. Admin socket `0600`, group `thlibo-admin`. Sockets are not created until the daemon emits `{"status":"ready"}`.

## Model

`bartowski/gemma-4-E4B-IT-GGUF` (HuggingFace). `Q8_0` on GPU, `Q4_K_M` on CPU (~2.5 GB RAM). Recommended sampling: `temperature=1.0`, `top_p=0.95`, `top_k=64`. Context 128K. Multimodal: image content must precede text; image token budget ∈ {70, 140, 280, 560, 1120}. Single-turn only — no multi-turn thought-stripping needed.

Inference engine is **llamafile (Mozilla)**. Do not write a custom inference engine; `thlibod` spawns llamafile as a private child on stdio or a private localhost port.

## When adding code

- Follow the repo layout sketched in `.plan/thlibo-spec.md` §"Repo structure" (`cmd/thlibod`, `cmd/thlibo`, `internal/{daemon,ipc,processors,router,queue,adapters,install}`, `processors/` for embedded built-ins).
- Before declaring a v0.1 task "done", cross-check against the checklist in `.plan/thlibo-spec.md` §"v0.1 build checklist".
- When the spec and this file disagree, the spec wins — update this file to match.
