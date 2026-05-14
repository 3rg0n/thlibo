# CLAUDE.md

Guidance for AI assistants (Claude Code, Codex, etc.) working in this
repo. Humans: the README is your starting point; this file is for
agents that need architectural context in a single shot.

## Status

v0.2.0 released 2026-05-14. Two binaries shipped (`thlibo`,
`thlibod`), Claude Code hooks for Bash + PowerShell + Read tools,
Codex PostToolUse hook, full test + scanner CI on linux/macOS/Windows,
signed releases via Sigstore keyless, CycloneDX SBOM.

Authoritative sources when they disagree:

1. `.plan/thlibo-spec.md` — v0.1/v0.2 design document.
2. `THREAT_MODEL.md` — security posture + threat decisions.
3. `docs/adr/*.md` — cross-cutting architectural choices.
4. This file — drift happens; fix it when you see it.

## What this project is

Two-binary system that compresses AI-coding-assistant tool output
using a locally-hosted Gemma 4 E4B model:

- **`thlibod`** — inference daemon. Spawns `llamafile` as a private
  HTTP backend on a per-user Unix socket (/Windows named pipe), loads
  the model once, stays warm, serves newline-delimited JSON over IPC.
  Knows only about inference.
- **`thlibo`** — CLI + middleware. Subcommands: `rewrite`, `exec`,
  `compress`, `case`, `install`, `uninstall`, `pull`, `version`.
  Scans `~/.thlibo/processors/`, routes tool output to the right
  processor (script or prompt), posts fully-formed requests to the
  daemon, returns the compressed result. Knows only about routing.

## Architectural invariants (load-bearing — do not blur)

1. **Daemon has zero knowledge of processors, hooks, routing, or
   clients.** It accepts fully-formed `messages` arrays + sampling
   params and streams tokens back.
2. **Middleware has zero knowledge of llamafile, model loading, or
   inference mechanics.** It speaks only the daemon's JSON protocol.
3. **Fallback to original output on any error path.** The middleware
   must never break the AI client. Script non-zero exit, daemon
   unreachable, parse failure, timeout → pass through the original
   bytes. Every hook script exits 0 on error.
4. **Short-circuit before doing any work.** Input under 2000 chars
   passes through without scanning processors or calling the daemon.
5. **Fast-path before routing.** Each processor's `match` regex is
   checked before the daemon is asked to route — a regex hit
   dispatches immediately, no routing call.
6. **Single daemon instance.** Enforced by lock file with
   non-regular-file rejection. The daemon never restarts the model
   on its own; only `llamafile` crashes trigger restart (max 3).
7. **Concurrency is fixed: 1 active generation + 10 queued + 4 per
   caller.** `ErrFull` / `ErrCallerFull` returned immediately; client
   disconnect cancels the job.
8. **Thinking mode is owned by the processor prompt, not the
   daemon.** The daemon has no `thinking` toggle; Gemma 4's
   `<|channel>thought` block is stripped by `processors.Strip` before
   output reaches the AI client.
9. **Daemon is offline-only.** It does not reach the network.
   `thlibo pull` is the one network touch, by explicit user action.
10. **All hook scripts are SHA-stamped and survive reinstall.** User
    edits are preserved; new versions land at `<path>.new` on
    conflict.

## Processors

Live in `~/.thlibo/processors/<name>/`. Two kinds:

- `processor.yaml` → **script processor**. `entry` is a plain
  filename (`.py` → python3, `.sh` → bash, `.exe`/`.bin` → direct).
  stdin in, stdout out, non-zero exit = fallback. Entry is
  fingerprinted (size/mtime/mode) at load and re-verified at
  dispatch — TOCTOU guard.
- `processor.md` → **prompt processor**. YAML frontmatter is config
  (`temperature`, `max_tokens`, `match`, `thinking`, etc.); the
  markdown body is the system prompt, sent to the daemon verbatim.
- Both present → yaml wins for type, md body is the system prompt.
- Neither → folder ignored.

Built-ins (`compress`, `casefolder`, `git-filter`, `npm-filter`,
`cargo-filter`) are embedded via `go:embed`. A user processor of
the same name overrides a built-in; the registry emits a
`ShadowWarning` at load time so it's visible.

## IPC

| Platform | Inference endpoint              | Admin endpoint                  |
|----------|---------------------------------|---------------------------------|
| Linux    | `/run/thlibo/infer.sock`        | `/run/thlibo/admin.sock`        |
| macOS    | `$TMPDIR/thlibo/infer.sock`     | `$TMPDIR/thlibo/admin.sock`     |
| Windows  | `\\.\pipe\thlibo-infer`         | `\\.\pipe\thlibo-admin`         |
| Fallback | `127.0.0.1:47320` (loopback)    | —                               |

Permissions: infer socket mode `0660`, group `thlibo-users` (if the
group exists; graceful degrade to user-only if not). Admin socket
`0600`. Sockets are not created until the daemon emits
`{"status":"ready"}`. On connect, the daemon does a second identity
check via `SO_PEERCRED` (Linux) / `GetNamedPipeClientProcessId` +
`OpenProcessToken` (Windows) and rejects UID/SID mismatches.

NDJSON frames have a 64 MiB per-frame cap; oversized frames get
`ipc.ErrFrameTooLarge` and the connection is dropped.

## Model

`unsloth/gemma-4-E4B-it-GGUF/gemma-4-E4B-it-UD-Q4_K_XL.gguf`
(HuggingFace). 5.1 GB. SHA-256 pinned into the binary at build time
via `-ldflags -X`. Not attached to GitHub releases (exceeds 2 GiB
cap); users fetch via `thlibo pull`.

Sampling defaults from the Gemma 4 model card: `temperature=1.0`,
`top_p=0.95`, `top_k=64`. Context 128K. `thlibod -ctx` defaults to
32768; overridable. Stop tokens `<turn|>` + `<end_of_turn>` passed
to llamafile via `--stop`.

Inference engine is **llamafile (Mozilla)** running in `--server`
mode, bound to a private Unix socket. thlibod speaks HTTP to it over
the socket. Do not write a custom inference engine.

## Adapters

- **`internal/adapters/claudecode/`** — PreToolUse hooks for Bash,
  PowerShell, and Read tools. Settings merger. /caselog skill.
- **`internal/adapters/codex/`** — PostToolUse hook using
  `decision: block` + `reason` to substitute the tool result.

## When adding code

- Repo layout: `cmd/thlibo`, `cmd/thlibod`, `internal/*` (adapters,
  casefile, daemon, execpolicy, install, ipc, logx, middleware,
  processors, promptsan, queue, router, shellcmd, update, version),
  `processors/` for embedded built-ins, `skills/` for Claude Code
  skill definitions.
- New user-facing features: add a scanner annotation if one fires
  (gosec / semgrep / staticcheck all block CI). Keep `#nosec` and
  `nosemgrep` reasons short but honest.
- New subcommands: wire into `cmd/thlibo/main.go` switch, update the
  usage string, and exclude from the update-check short-circuit
  only if the subcommand should NOT trigger a background update
  fetch (like `version`).
- When the spec and this file disagree, the spec wins — and update
  this file.

## Two Claude sessions?

When two Claude Code sessions share this repo (Windows + macOS QA
pairing, etc.), treat GitHub Issues as the source of truth and
always `git fetch origin && git rebase origin/main` before every
local commit. Reference issues by `Fixes #N` / `Refs #N` in commit
messages so the timeline stays legible. If you see a commit you
didn't make against a file you're mid-edit on, stop and ask before
pushing.
