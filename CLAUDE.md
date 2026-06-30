# CLAUDE.md

Guidance for AI assistants (Claude Code, Codex, etc.) working in this
repo. Humans: the README is your starting point; this file is for
agents that need architectural context in a single shot.

## Status

v0.7.7 (current). Single binary shipped (`thlibo`); inference runs in
a separate sidecar, **`inferd`** (its own repo, github.com/3rg0n/inferd),
which `thlibo install` probe-or-installs. `thlibo install` is zero-touch
on all three OSes (incl. Windows arm64): it copies inferd's `backends/`
libs beside the daemon, pins the latest *stable* (non-`-rc`) inferd, runs
the per-user installer (LaunchAgent / systemd-user / Startup-shortcut),
and probes the daemon for readiness before reporting success
(fresh-install fixes, #47). `thlibo upgrade` rename-then-replaces its own
binary so it works while running (#52). Claude Code hooks for Bash +
PowerShell + Read + Write/Edit tools; Codex PostToolUse hook (writes the
canonical `[features] hooks` flag + reminds you to trust it via `/hooks`,
#57). Full test + scanner CI on linux/macOS/Windows, signed releases via
Sigstore keyless, CycloneDX SBOM.

> History: through v0.5.x thlibo shipped a second binary, `thlibod`,
> that spawned llamafile directly. ADR 0005 extracted all inference
> into `inferd`; ADR 0006 made thlibo fail open during the inferd
> bootstrap window. If you see `thlibod`, llamafile, or `thlibo pull`
> referenced as live, that's stale — they're gone.

Authoritative sources when they disagree:

1. `THREAT_MODEL.md` — security posture + threat decisions.
2. `docs/adr/*.md` — cross-cutting architectural choices.
3. This file — drift happens; fix it when you see it.

## What this project is

A single binary plus PreToolUse hooks that compresses
AI-coding-assistant tool output, backed by a locally-hosted Gemma 4
E4B model served by a sidecar:

- **`thlibo`** — CLI + middleware (the whole repo). Subcommands:
  `rewrite`, `exec`, `compress`, `case`, `shorthand`, `install`,
  `uninstall`, `upgrade`, `config`, `version` (see `cmd/thlibo/main.go`
  for the authoritative switch). Scans `~/.thlibo/processors/`, routes
  tool output to the right processor (script or prompt), and — for
  prompt processors — posts fully-formed requests to inferd. Knows
  only about routing; never about model loading or inference
  mechanics.
- **`inferd`** — inference sidecar, separate project. Loads the model
  once, stays warm, serves the length-prefixed v2 wire over a per-user
  socket. thlibo talks to it through `internal/inferd` — thlibo's own
  codec implemented against inferd's `protocol-v2.md` (no dependency on
  inferd's reference client). If inferd is unreachable, the middleware
  fails open (passthrough) per ADR 0006.

## Architectural invariants (load-bearing — do not blur)

1. **Middleware has zero knowledge of model loading or inference
   mechanics.** It speaks only inferd's wire protocol via
   `internal/inferd` (thlibo's owned codec). (Inference invariants —
   single warm model, fixed concurrency, offline-only generation — now
   live in the `inferd` repo, not here.)
2. **Fallback to original output on any error path.** The middleware
   must never break the AI client. Script non-zero exit, inferd
   unreachable, parse failure, timeout → pass through the original
   bytes. Every hook script exits 0 on error. (ADR 0006 — fail open.)
3. **Short-circuit before doing any work.** Input under
   `middleware.MinBytesForRouting` (2000 bytes) passes through without
   scanning processors or calling inferd.
4. **Fast-path before routing.** Each processor's `match` regex is
   checked before inferd is asked to route — a regex hit dispatches
   immediately, no routing call.
5. **Thinking mode is owned by the processor prompt, not inferd.**
   Gemma 4's `<|channel>thought` block is stripped by the
   `internal/processors` thinking filter (`thinking.go`) before output
   reaches the AI client.
6. **All hook scripts are SHA-stamped and survive reinstall.** User
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
  markdown body is the system prompt, sent to inferd verbatim.
- Both present → yaml wins for type, md body is the system prompt.
- Neither → folder ignored.

Built-ins are embedded via `go:embed` under `processors/` (see
`processors/embed.go`): `compress`, `casefolder`, `shorthand` (prompt
processors) plus the deterministic script filters `git-filter`,
`npm-filter`, `cargo-filter`, `pytest-filter`, `ndjson-filter`,
`stacktrace-filter`, `lint-filter`, `trivy-filter`, `cordon-filter`,
and `pdf-to-md`. A user processor of the same name overrides a
built-in; the registry emits a `ShadowWarning` at load time so it's
visible.

## Talking to inferd

thlibo is a *client* of inferd; it does not own the model or the
inference mechanics. But it **does** own its wire codec — implemented
directly against inferd's `protocol-v2.md` (length-prefixed `0x01`/
`0x02` framing, in-band `wire_version`, the unified generation socket),
not via inferd's reference Go client. That's a deliberate decoupling
(ADR-level: thlibo's release no longer waits on inferd's client
cadence). The surface is `internal/inferd`:

- `protocol.go` — wire types (`Request`/`Message`/`Result`/
  `ResponseFormat`/`Tool`) + the length-prefixed frame reader/writer.
- `client.go` — `Post(ctx, Request) (Result, error)`: dial, stream,
  collapse to text + tool calls; fail-open on connect/parse failure.
- `addr.go` — socket resolution per `protocol-v2.md` §1.1
  (`\\.\pipe\inferd` / `inferd.sock`, XDG→$HOME→/tmp).
- `dial_unix.go` / `dial_windows.go` — UDS / named-pipe dialers
  (no TCP — inferd binds no network listener, ADR 0022).

If you need to change how thlibo *reaches* or *frames* inference,
that's here; if you need to change inference behaviour (model,
sampling, concurrency, queueing), that's the inferd repo. If inferd
bumps `wire_version`, the daemon fails the request loudly and this is
where you update.

The middleware sends prompt-processor work to inferd as a
fully-formed request and gets compressed text back. The router uses
`response_format` (JSON-Schema) to constrain routing output. On any
failure to reach or parse, it fails open (ADR 0006).

## Adapters

- **`internal/adapters/claudecode/`** — PreToolUse hooks for Bash,
  PowerShell, Read, and Write/Edit tools. Settings merger. /caselog
  skill.
- **`internal/adapters/codex/`** — PostToolUse hook using
  `decision: block` + `reason` to substitute the tool result.

## Build, test, scan

```
go build ./...                 # build all
go build -ldflags "-X github.com/3rg0n/thlibo/internal/version.Tag=v0.X.Y" -o thlibo ./cmd/thlibo
go test ./...                  # full suite
go test ./internal/middleware/... -run TokenSavings   # the savings benchmark
go vet ./...                   # required before commit
staticcheck ./...              # required — blocks CI
gosec ./...                    # required — blocks CI
```

The version tag is injected via `-ldflags -X …/internal/version.Tag`;
an un-injected build reports `dev` and skips the background
update-check.

## When adding code

- Repo layout: `cmd/thlibo` (the only binary), `internal/*`
  (adapters, casefile, config, execpolicy, inferd, install, logx,
  middleware, processors, promptsan, router, shellcmd, shorthand,
  update, version), `processors/` for embedded built-ins, `skills/`
  for Claude Code skill definitions.
- New user-facing features: add a scanner annotation if one fires
  (gosec / semgrep / staticcheck all block CI). Keep `#nosec` and
  `nosemgrep` reasons short but honest.
- New subcommands: wire into `cmd/thlibo/main.go` switch, update the
  usage string, and exclude from the update-check short-circuit
  only if the subcommand should NOT trigger a background update
  fetch (like `version`).
- `.plan/thlibo-spec.md` is the original v0.1/v0.2 design doc — useful
  history, but the ADRs (`docs/adr/`) outrank it for anything the
  inferd extraction touched. When an ADR and this file disagree, the
  ADR wins — and update this file.

## Two Claude sessions?

When two Claude Code sessions share this repo (Windows + macOS QA
pairing, etc.), treat GitHub Issues as the source of truth and
always `git fetch origin && git rebase origin/main` before every
local commit. Reference issues by `Fixes #N` / `Refs #N` in commit
messages so the timeline stays legible. If you see a commit you
didn't make against a file you're mid-edit on, stop and ask before
pushing.
