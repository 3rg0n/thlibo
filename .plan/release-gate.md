# Release Gate

> Source of truth: [`thlibo-spec.md`](./thlibo-spec.md). Every requirement below traces to a spec section. If a row here and the spec disagree, the spec wins — update this file.
>
> **Release rule:** every row's pass condition must be met. No partial passes, no "we'll do it next release". Starting version is 0.1.0; whatever version number we hold when the last row flips to ✅ is the one we cut.
>
> **Verification vocabulary:**
> - **UT** — unit test, runs in `go test ./...`
> - **IT** — integration test, spins a real daemon + real socket (gated by build tag `integration`)
> - **E2E** — end-to-end against a real AI client (Claude Code) with a real model loaded
> - **Manual** — documented procedure a human runs once per release candidate; result recorded in `.plan/release-notes-<version>.md`

---

## A. Daemon (`thlibod`)

| # | Requirement | Spec | Verification | Pass condition |
|---|---|---|---|---|
| A1 | Spawns llamafile as a private child (stdio or private localhost port). Never exposes llamafile externally. | §Inference engine, §Daemon lifecycle | IT + Manual | IT: daemon process tree shows llamafile as child; no listening TCP port other than fallback socket (if enabled). Manual: `ss -tlnp` (Linux) / `netstat -ano` (Windows) shows no llamafile-owned listener. |
| A2 | Single-instance lock. Second `thlibod` start exits non-zero without touching the running instance. | §Daemon lifecycle step 1 | IT | Start daemon, start a second `thlibod`, assert second exits non-zero and first is unaffected. |
| A3 | IPC endpoints created with correct path, group, and mode per platform. | §IPC endpoints | IT (Linux/macOS), Manual (Windows) | Linux: `infer.sock` is mode 0660, group `thlibo-users`. `admin.sock` is mode 0600, group `thlibo-admin`. macOS: same under `/var/run/thlibo/`. Windows: named pipe ACL denies Everyone, grants current user. |
| A4 | Sockets not created until model is ready. | §Daemon lifecycle step 4-5 | IT | During model load, connect attempt fails (ENOENT/pipe-not-found). After `{"status":"ready"}` on admin socket, connect succeeds. |
| A5 | Newline-delimited JSON request/response protocol matches spec exactly. | §Request protocol | UT + IT | UT: encoder/decoder round-trips every documented request and response shape. IT: send a real request, receive `status` → `token`* → `done` frames in order, newline-terminated. |
| A6 | Sampling defaults: `temperature=1.0`, `top_p=0.95`, `top_k=64` applied when caller omits them. | §Request protocol | UT | Omit each field in request; assert downstream llamafile invocation uses the documented default. |
| A7 | Image token budget accepts only {70, 140, 280, 560, 1120}; other values error. | §Request protocol, §Gemma 4 reference | UT | Each valid value accepted; any other value returns `{"type":"error"}` without reaching llamafile. |
| A8 | Queue: 1 active generation, 10 queued max. | §Concurrency | IT | Submit 12 requests concurrently. Assert exactly 1 active at a time, 10 queued, 12th receives immediate `{"type":"error","message":"queue full"}` (or equivalent). |
| A9 | Client disconnect cancels the in-flight job for that client. | §Concurrency | IT | Start a long generation, drop the socket mid-stream, assert llamafile stops producing tokens for that job within 500 ms and the queue advances. |
| A10 | Health transitions: daemon emits `loading_model` then `ready` on admin socket. | §Request protocol response types | IT | Connect to admin socket at start; observe `{"status":"loading_model"}` then `{"status":"ready"}`. |
| A11 | llamafile crash → restart, **max 3 total restart attempts** across daemon lifetime, then error on admin socket and no further restarts. | §Daemon lifecycle step 8, §Security | IT | Kill llamafile child repeatedly; assert daemon restarts it for attempts 1, 2, 3. On the 4th crash, daemon emits `{"type":"error"}` on admin socket and does not spawn a 5th llamafile. Counter resets only on daemon restart. |
| A12 | Graceful shutdown on SIGTERM (Linux/macOS) or equivalent (Windows): drain queue, stop child, release lock, exit 0. | §Daemon lifecycle step 9 | IT | Send SIGTERM with 3 queued jobs; assert all 3 complete, child exits, lock file removed, exit code 0. |
| A13 | System prompts travel only on inference socket, never on admin socket. | §Security | UT | Admin protocol has no `messages` field; attempts to send one are rejected. |

## B. Middleware (`thlibo`) — core flow

| # | Requirement | Spec | Verification | Pass condition |
|---|---|---|---|---|
| B1 | Input < 2000 chars passes through unchanged, no daemon call, no processor scan. | §Main flow | UT | Feed 1999-char input; assert output is byte-identical and daemon mock receives zero calls. |
| B2 | Processor registry loaded from `~/.thlibo/processors/` at startup. | §Processors | UT | Populate temp dir with 3 valid processors and 1 bogus folder; assert registry has exactly 3 entries, bogus one ignored. |
| B3 | Descriptor precedence: `yaml` wins for type, `md` body becomes system prompt when both present. | §Descriptor rules | UT | Folder with both files: registry entry has `type=script`, body field populated from md. |
| B4 | Fast-path regex match dispatches without a routing call. | §Routing decision flow step 1 | UT | Input matches processor's `match`; assert daemon-routing mock receives zero calls, processor runs. |
| B5 | Routing call sent to daemon when no fast-path match. Uses Gemma 4 native tool-call format (`<\|tool_call>call:route{processors:[...]}<tool_call\|>`) with a GBNF grammar enforcing the shape. Registry names + first 200 chars of input passed in. | §Routing, §Router tool-call format | UT + IT | UT: tool declaration + grammar match fixtures; regex parser handles single, chain, and empty-chain responses per spec. IT: live daemon returns a tool-call; middleware dispatches to it. |
| B6 | Router returning `"none"` → pass through original input unchanged. | §Routing decision flow step 2-3 | UT | Mock daemon returns `"none"`; output is byte-identical to input. |
| B7 | Processor chaining: router returns `"a,b"` → run a, pipe result into b. | §Routing decision flow step 3 | UT | Mock daemon returns `"git-filter,compress"`; assert `compress.Run` receives `git-filter`'s output, not original. |
| B8a | Fallback: daemon unreachable → output == input, exit 0. | §Main flow | UT | As stated. |
| B8b | Fallback: daemon routing call times out → output == input, exit 0. | §Main flow | UT | Timeout is configurable; default documented in README. |
| B8c | Fallback: router returns an unknown processor name → output == input, exit 0. | §Main flow | UT | No crash, no partial-processor run. |
| B8d | Fallback: script processor exits non-zero → output == input, exit 0. | §Main flow, §Entry file execution | UT | Stderr captured to debug log but not surfaced to client. |
| B8e | Fallback: script processor crashes (signal/panic/unhandled exception) → output == input, exit 0. | §Main flow | UT | Parent reaps child cleanly. |
| B8f | Fallback: prompt processor (daemon inference) times out mid-stream → output == input, exit 0. | §Main flow | UT | Partial tokens discarded; no truncated response leaks to client. |
| B8g | Fallback: descriptor parse error discovered at dispatch time → output == input, exit 0, processor quarantined for the rest of the run. | §Main flow, §Descriptor rules | UT | Registry log records the parse failure. |
| B8h | Fallback: empty registry (no built-ins, no user processors) → output == input, exit 0. | §Main flow | UT | Should never happen in practice (built-ins always embedded), but defensively covered. |

## C. Processors

| # | Requirement | Spec | Verification | Pass condition |
|---|---|---|---|---|
| C1 | Script processor dispatch: stdin in, stdout out, extension → interpreter map per spec. | §Entry file execution | UT + IT | UT per extension (`.py`, `.sh`, `.exe`/`.bin`). IT runs the `git-filter` built-in against a captured `git status` fixture. |
| C2 | Script processor non-zero exit → fallback to original (tested by B8, called out separately for traceability). | §Entry file execution | UT | Covered by B8; explicit row ensures it's not lost. |
| C3 | Prompt processor request built from frontmatter (`temperature`, `max_tokens`, `thinking`, etc.) + md body as system prompt. | §Processors — prompt type | UT | Fixture `processor.md` → assert resulting daemon request matches fixture byte-for-byte (excluding the `user` content). |
| C7 | **Thought-stripping applied to every prompt processor response.** Strip `<\|channel>thought…<channel\|>` blocks (including the empty block present when thinking is disabled). | §Thinking mode — Thought-stripping | UT | Responses with empty thought block, single thought block, and multiple thought blocks all return answer-only content. Raw input without thought blocks passes through unchanged. |
| C4 | Built-in processors (`compress`, `casefolder`, `git-filter`, `npm-filter`, `cargo-filter`) embedded in binary and loaded when `~/.thlibo/processors/` is empty or missing. | §Built-in processors | UT + Manual | UT: embedded fs has all 5. Manual: fresh machine with no `~/.thlibo/processors/` still routes `git status` through `git-filter`. |
| C5 | User processor with same name as a built-in overrides the built-in. | §Built-in processors | UT | Registry populated with built-ins, then user `git-filter/` added; registry returns user version. |
| C6 | All 5 built-in processors produce output on their representative fixture input. | §Token savings estimate | IT | One fixture per processor (git status, npm list, cargo test, stack trace, generic verbose log). Each produces non-empty, non-identical output. |

## D. Client adapters

| # | Requirement | Spec | Verification | Pass condition |
|---|---|---|---|---|
| D1 | Claude Code `PreToolUse` hook adapter: hook script receives Bash tool input on stdin, calls `thlibo rewrite`, emits `updatedInput` JSON per Claude Code docs. Subprocess stdout is compressed by the time Claude Code captures it. | §Client adapters | UT + IT + E2E | UT: hook-script and `thlibo rewrite` logic tested with fixtures. IT: end-to-end through `thlibo exec` against a real `git status` fixture. E2E: with hook installed in `~/.claude/settings.json`, a Claude Code session triggering a Bash call produces a compressed tool_output and no hook-error banner. |
| D2 | Codex adapter uses its equivalent PreToolUse-style hook mechanism. | §Client adapters | IT + Manual | IT: faked Codex hook protocol (stdin envelope shape recorded from a real Codex session) round-trips through adapter. Manual once-per-release: if Codex is installed on the release machine, record a smoke-test in release notes; if not, note "Codex E2E deferred — not available on release machine" and file a follow-up. |
| ~~D3~~ | ~~Proxy mode binds `127.0.0.1:47321`, intercepts Read/Glob/Grep, passes everything else.~~ | ~~§Client adapters~~ | **Deferred to v0.2.** | The PreToolUse-rewrite mechanism covers Bash-tool output (matches every row in the spec's token-savings table). `Read`/`Grep`/`Glob` compression via `ANTHROPIC_BASE_URL` proxy is a post-v0.1 candidate once real-world coverage gaps are known. |

## E. Installer (`thlibo install`, `thlibo pull`)

| # | Requirement | Spec | Verification | Pass condition |
|---|---|---|---|---|
| E1 | `thlibo install` creates `thlibo-users` group, adds current user. | §Install flow | Manual | Fresh VM per OS; after install, `getent group thlibo-users` (Linux) / `dscl . -read /Groups/thlibo-users` (macOS) contains the invoking user. Windows equivalent documented. |
| E2 | `thlibo install` writes service unit and starts daemon. | §Install flow | Manual | Linux: systemd unit at documented path, `systemctl status thlibod` active. macOS: launchd plist loaded. Windows: Service installed and running. |
| E3 | `thlibo install` creates socket directory with correct ownership and populates `~/.thlibo/processors/` with built-ins on disk (so users can edit). | §Install flow, §Directory layout | Manual | Directory present with all 5 built-ins visible as files. |
| E4 | `thlibo install` writes PostToolUse hook to `~/.claude/settings.json` without clobbering existing hooks. | §Install flow | UT + Manual | UT: merge logic preserves unrelated keys. Manual: install on a machine with existing settings.json; other hooks still present. |
| E5 | `thlibo pull gemma-4-e4b` downloads the Q4_K_M GGUF to `~/.thlibo/models/` with a progress indicator and verifies integrity. | §Install flow | Manual | Download completes, file SHA matches published hash, progress bytes shown. Re-running is idempotent (no re-download). |

## F. Cross-cutting

| # | Requirement | Spec | Verification | Pass condition |
|---|---|---|---|---|
| F1 | ✅ Builds on Linux, macOS, Windows without platform-specific build steps beyond a single `go build` target per OS. | §IPC endpoints table | CI | CI matrix (`.github/workflows/ci.yml`) covers ubuntu-latest, macos-latest, windows-latest on Go 1.22 with `go vet`, `go build`, `go test`. Release workflow (`.github/workflows/release.yml`) cross-compiles linux-amd64/arm64, darwin-arm64, windows-amd64 on `v*` tag push. First green run on main closes this. |
| F2 | ✅ `go vet ./...`, `staticcheck ./...`, `govulncheck ./...`, `gosec ./...`, `semgrep --config=auto .` clean. | Global instructions | CI | `scanners` job in `ci.yml` runs all five plus gitleaks (via `secrets` job). Zero findings locally across shipped code (`cmd/`, `internal/`, `processors/`). First green run on main closes this. |
| F3 | ✅ No secrets in repo; `.gitignore` covers `*.gguf`, `*.pem`, `*.key`, `.env*`, `dist/`, `bin/`, `.references/`, `.test/`. | Global instructions | UT | gitleaks hook on commit + gitleaks job in CI. |
| F4 | ✅ `CHANGELOG.md` exists with per-phase entries. Version header (`[0.1.0]`) added at tag time. | Global instructions | Manual | Review before tagging. |
| F5 | ✅ README covers what thlibo is, install from source (Windows + Unix variants), `thlibo install` flags, uninstall by hand, custom processor tutorial, healthcheck, disable, security model, known limitations. | (spec implicit) | Manual | Walked the README on Windows; fresh install produced 98.5% compression on `git diff HEAD~5`. |
| F6 | Token savings measured and recorded. | §Token savings estimate | Manual | Each row in the spec's savings table reproduced with a documented fixture. Actual pre/post token counts recorded in release notes. No numeric threshold — the gate is "we measured and published the numbers," not "we hit a target." Large deviations from spec estimates should be called out in release notes but don't block release. |

---

## Out of scope for this gate

Called out so scope creep doesn't sneak them in:

- Multi-turn conversations (spec: single-turn only in v0.1).
- Tool access inside processors (spec: "text tasks only — no tool access in v0.1").
- Platform support beyond Linux/macOS/Windows.
- Auth beyond socket group membership.
- Metrics/telemetry export (daemon has no OpenTelemetry hooks in this cut).
- Model swapping at runtime — one warm model per daemon lifetime.

## Release procedure

When every row above is ✅:

1. Tag the version.
2. Update `CHANGELOG.md` `[Unreleased]` → concrete version + date.
3. Attach the completed gate doc (this file with all rows ticked) to the release notes.
4. Cut binaries for the CI matrix.

If a row cannot be passed as written, **change the spec and this doc first**, get confirmation, then implement. Don't silently weaken a gate row to make it pass.
