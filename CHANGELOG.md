# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `thlibo case <file>` subcommand and `/caselog` Claude Code skill +
  PreToolUse Read-tool hook. Flow: when Claude's `Read` tool fires
  on a log-shaped file over ~32 KB, the hook calls `thlibo case`
  which writes `~/.thlibo/cases/<ts>-<hex>/{compressed.log,
  summary.md, meta.json}` and rewrites `tool_input.file_path` to
  the compressed variant. Claude sees the small version without
  the user having to pre-process. Users can also invoke the
  `/caselog` skill manually or run `thlibo case <file>` on the
  CLI. Gated on extension (`.log|.ndjson|.txt|.out|.err|.trace|
  .dump`), size (`THLIBO_READ_MIN_BYTES`, default 32 KiB), and
  honours `$THLIBO_DISABLED`. Falls back silently on any error so
  Claude always gets something to read. Includes
  `thlibo case --prune <duration>` to garbage-collect old cases.
  The skill SKILL.md is mirrored into
  `~/.claude/skills/caselog/SKILL.md` by `thlibo install`, using
  the same SHA-stamp / conflict-preservation semantics as hook
  scripts.
- Hook scripts survive `thlibo install` across updates. `WriteHookScript`
  and `WriteHookScriptPS1` now stamp the installed file with a SHA-256
  comment (`# thlibo-installed-sha: <hash>`). On reinstall: if the
  embedded version is unchanged the file is left untouched (Unchanged);
  if the file is pristine (user never edited it) it is overwritten with
  the new version (Updated); if the user modified the file the new
  version is written alongside as `<path>.new` and the user's file is
  preserved (Conflict). `thlibo install` prints a clear message for each
  case. Closes #12.
- Background update checker. `thlibo` CLI invocations (everything
  except `thlibo version`) fire a detached goroutine that fetches
  `api.github.com/repos/3rg0n/thlibo/releases/latest` once per
  cooldown window (default 24 h, cached at
  `~/.thlibo/state/update-check.json`). When a newer semver tag is
  available, prints a single banner to stderr pointing at the
  install-script upgrade command; the banner repeats on subsequent
  invocations only if the latest tag changes. Network failures are
  silent (logged at debug). Kill switches: `THLIBO_NO_UPDATE=1` or
  `THLIBO_UPDATE_INTERVAL=0`. Dev builds (untagged) never check.
- New `internal/version` package exposing the build tag via
  `-ldflags -X github.com/3rg0n/thlibo/internal/version.Tag=v…`;
  `release.yml` now injects `${{ github.ref_name }}` on every build.
- New `thlibo version` / `thlibo --version` subcommand prints the
  embedded tag without triggering the update check.

### Fixed

- macOS Gatekeeper no longer blocks thlibo/thlibod on first run.
  `install.sh` strips `com.apple.quarantine` from both binaries after
  extraction; `PullEngine` does the same for the llamafile engine after
  download. Without this, macOS pops an "app blocked" toast and the
  daemon cannot start. Closes #13.
- `install.sh` now passes `GITHUB_TOKEN` (when set) as an
  `Authorization: Bearer` header to the GitHub releases API call that
  resolves `latest`. Machines that have exhausted the 60-req/hr
  unauthenticated rate limit were getting a 403 and the installer
  bailed before downloading anything. Explicit `THLIBO_VERSION=vX.Y.Z`
  still bypasses the API entirely. Closes #14.

- LaunchAgent plist now sets `HOME` env var and passes explicit `-engine`/`-model`
  flags so thlibod doesn't fail to resolve default paths under launchd's stripped
  environment. Daemon was crashing in a restart loop with `engine exited before
  ready: exit status 1` on every launchd-managed start. Closes #11.
- `THLIBO_LOG=1` baked into the plist so activity logs appear in
  `~/.thlibo/logs/thlibod.ndjson` without manual configuration.
- `install.sh` NEXT STEP 2 now instructs `--pull-engine --pull-model`
  together; the engine (~838 MB) is required and was silently omitted
  from the documented flow, causing `thlibod` to fail immediately on
  fresh installs with "no such file or directory". Closes #5.
- `thlibo install` plan output now clearly warns when the engine will
  not be downloaded ("thlibod will fail without it") instead of the
  neutral "not downloaded" message.
- Remove unused plist XML structs (`launchAgent`, `dictNode`, `kvNode`)
  in `internal/install/autostart_darwin.go` — leftover from an earlier
  draft before `plistXML()` switched to hand-rolled string formatting.
  Closes #10.

## [0.2.0] - 2026-05-14

Hardening + platform-coverage release. Closes every finding from the
MAESTRO threat model sweep, including the four originally marked
"Accepted by design" that were re-scoped as the `inferd` split
surfaced a multi-tenant story. Adds PowerShell tool support for
Claude Code on Windows, Gemma 4 native context-window + stop-token
flags for the daemon, and Sigstore keyless signing + CycloneDX SBOM
on every release artefact.

### Added (v0.2 hardening — #16, #17, #22, #24)

- `thlibo uninstall` subcommand reverses `thlibo install`: removes
  thlibo entries from `~/.claude/settings.json` via new
  `claudecode.RemoveHooks`, deletes hook scripts, unregisters
  autostart. `--purge` additionally deletes `~/.thlibo/` (processors,
  models, logs). `--dry-run` prints the plan without touching the
  filesystem. Closes finding #16.
- `$THLIBO_DISABLED=1` env gate honoured by `hook.sh`, `hook.ps1`,
  and Codex `hook.sh`. Per-session bypass without uninstalling.
- `queue.NewWithCallerCap` adds a per-caller concurrent-queued
  quota (default 4) on top of the global 10-slot limit. Exceeding
  the cap returns new `queue.ErrCallerFull`. Closes finding #17.
- `internal/execpolicy` package: loads `~/.thlibo/policy.yaml`
  (override via `$THLIBO_POLICY`), evaluates against `argv[0]`
  with deny-wins semantics and configurable default. `thlibo exec`
  calls it before spawn; denial returns exit 77 (`EX_NOPERM`).
  Closes finding #22.
- `ipc.PeerIdentity(net.Conn) (PeerID, error)` reads
  `SO_PEERCRED` on Linux and `GetNamedPipeClientProcessId` +
  `OpenProcessToken` on Windows. Darwin path returns a bare Unix
  transport identity with `UID=-1` until `LOCAL_PEERCRED` lands
  in v0.3. Daemon rejects UID/SID mismatches at accept time.
  Closes finding #24.

### Added (v0.2 feature work)

- **PowerShell tool support (#12).** Embedded `hook.ps1` companion to
  `hook.sh`; `MergeSettingsFull` registers a second PreToolUse
  matcher for `PowerShell` pointing at the new hook via
  `powershell -NoProfile -ExecutionPolicy Bypass -File <path>`.
  Installer writes both hooks unconditionally so Claude Code picks
  up whichever tool the current session uses
  (`CLAUDE_CODE_USE_POWERSHELL_TOOL=1` selects PowerShell).
- **Gemma 4 context window + stop tokens wired into the daemon (#13).**
  New `thlibod -ctx N` (default 32768, passed as `-c <N>`) and
  `thlibod -stop "<t1>,<t2>"` (default `<turn|>,<end_of_turn>`, each
  passed as `--stop <t>`) flags. Operator-supplied `-engine-args`
  appears after the built-in flags so last-value-wins overrides
  work.
- **Signed releases via Sigstore keyless (#27).** `release.yml` now
  runs `cosign sign-blob --yes` on every archive, `SHA256SUMS`, and
  the new SBOM. `.sig` + `.pem` uploaded alongside each asset.
  Identity = this workflow at the release tag; transparency log
  entries published to `rekor.sigstore.dev`.
- **CycloneDX SBOM on release (#28).** `anchore/sbom-action` emits
  `thlibo-sbom.cdx.json` at release time, pinned by commit SHA,
  signed with cosign alongside the other artefacts.

### Security

Second remediation pass sweeping every low-severity finding that is a
real bug (not a design decision). Combined with the first pass, the
remaining open items in `THREAT_MODEL.md` are the four explicitly
marked "won't-fix: by design" (queue-based rate limiting, exec
allow-list, SO_PEERCRED, persistent hook install) — #27 and #28 are
now closed.

### Added (second pass)

- Script-entry TOCTOU guard — `processors.EntryFingerprint`
  (size/mtime/mode) captured at registry load and re-verified at
  dispatch; a mismatch returns `processors.ErrEntrySwapped` and the
  middleware falls back. Closes finding #9.
- Rolling log rotation — keeps `.old`, `.old.1`, `.old.2` generations
  (configurable cap `maxRotatedGenerations`), preserving a forensics
  window that survives a second rotation. Closes finding #13.
- `processors.Strip` rewritten as a bounded state-machine parser
  with a `maxThoughtBytes = 64 KiB` cap on each block. Unclosed /
  oversized blocks now fall through as literal text instead of
  being eaten. Regression tests for stacked-open and oversized
  cases. Closes finding #19.
- `AcquireLock` rejects pre-existing symlinks at the lock path via
  `Lstat` + post-open `Stat.IsRegular()` check. Closes finding #21.
- Clarified `internal/install` package docstring: `thlibo install`
  does NOT create the `thlibo-users` group (v0.1 is per-user only).
  Closes finding #26.

### Changed (second pass)

- `SubprocessEngine.Generate` composes the child-request line via
  `json.Marshal` on an anonymous struct instead of `fmt.Sprintf`
  with `%q`. `%q` uses Go string-literal escape rules that don't
  match JSON for some edge cases (U+2028/2029, surrogate pairs).
  Closes finding #18.

### Security

Remediation sweep informed by the new MAESTRO threat model
(`THREAT_MODEL.md`). Each item links to the finding number it
addresses.

### Added

- `internal/promptsan` package — escapes Gemma 4 native tool-call
  markers (`<|`, `|>`) in untrusted tool output before it becomes a
  model-facing user turn. Used by both the middleware's prompt
  processors and the router. Closes finding #1.
- Dependency CVE pass via `govulncheck` returned 0 findings; wired a
  Dependabot config (`.github/dependabot.yml`) to track GitHub Actions
  SHA drift. Closes governance gap behind finding #2.
- `ipc.MaxRequestBytes` (64 MiB) per-frame cap with
  `ipc.ErrFrameTooLarge` on the daemon-side reader. Closes finding #5.
- `StartLimitIntervalSec=60 StartLimitBurst=3` + a full
  `NoNewPrivileges` / `ProtectSystem` / `ProtectHome` / `PrivateTmp` /
  `PrivateDevices` defence-in-depth block in the systemd user unit.
  Closes findings #6 and #14.
- `processors.ShadowWarning` emitted through the existing warnings
  channel when a user processor overrides a built-in of the same
  name. Closes finding #7.
- `logx.Redact` secret-pattern redactor applied to every `Str` /
  `Err` field (AWS keys, GitHub PATs, HuggingFace tokens, generic
  `*_TOKEN=` / `*_SECRET=` / `*_PASSWORD=` / `*_API_KEY=` assignments).
  Closes finding #8.
- Audit-trail NDJSON records for previously silent paths: daemon boot
  / ready / signal / stop, `thlibo pull` SHA mismatch or network
  failure, `parseRoutingResponseDetailed` surfacing unknown router
  names via `ClientAdapter.OnUnknownProcessor`. Closes findings
  #10, #11, #12.
- README "Security model" section explicitly names the auto-allow
  PreToolUse hook behaviour and points at `THREAT_MODEL.md` for the
  full trade-off discussion. Closes finding #15.
- `.gitignore` extra secret patterns: `*.jks`, `*.gpg`, `*.asc`,
  `*secret*`, `id_rsa*`, `id_ecdsa*`, `id_ed25519*`. Closes
  finding #20.

### Changed

- `verifySHA` now uses `crypto/subtle.ConstantTimeCompare` in place
  of the hand-rolled `equalFold` loop. Deleted `equalFold`. Closes
  finding #4.
- All GitHub Actions in `ci.yml`, `release.yml`, and `pages.yml` are
  pinned by commit SHA (with the semver tag preserved as a trailing
  comment). Closes finding #2.

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
