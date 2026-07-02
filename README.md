# thlibo

> From Greek *θλίβω* — to press, crush, compress.

**AI tool output compressor for local coding agents.** When Claude
Code runs `git status`, `npm install`, `cargo test`, or a verbose
log dump, thlibo intercepts the Bash-tool invocation, runs the real
command, and hands the AI a compressed version of the output —
preserving every load-bearing detail and dropping the noise.

Saves 60-99% of tokens on common dev commands without the AI
knowing the difference.

Uses the same PreToolUse+updatedInput pattern described in Claude
Code's hooks documentation: rewrite the Bash command before it runs
so the subprocess stdout is already compressed when the tool result
is captured. Same token savings, no proxy, no API-wire tampering.

```
 git diff HEAD~5        68821 bytes      ──thlibo──►    929 bytes     (98.6%)
 npm list 200 deps      ~6200 bytes      ──thlibo──►   ~600 bytes     (90%)
 cargo test failing     ~4800 bytes      ──thlibo──►   ~480 bytes     (90%)
 log file 500 lines    ~20000 bytes      ──thlibo──►  ~1500 bytes     (92%)
```

### Methodology

The savings figures on the [marketing page](https://3rg0n.github.io/thlibo/)
and in this README are measured in tokens — the unit you actually
pay for — not raw bytes:

- **Without thlibo** = what Claude Code consumes natively. Text
  outputs at [~4 chars/tok](https://docs.anthropic.com/en/docs/build-with-claude/tokens);
  PDFs at [~2,250 tok/page](https://docs.anthropic.com/en/docs/build-with-claude/pdf-support)
  (page render + extracted text).
- **With thlibo** = bytes returned by the processor, ÷4.

A 7-day log won't fit in any context window (Claude's is 200k tok).
The "without thlibo" figure for those rows is what the bytes
*would* tokenise to — in practice, without thlibo you wouldn't be
able to ask the model about that data at all.

Commands reproducible via
`go test ./internal/middleware/... -run TokenSavings`. PDF + log
fixtures embedded with [inferd](https://github.com/3rg0n/inferd)'s
300M-param embed model; top-percentile windows surfaced by k-NN
density (`CORDON_MAX_WINDOWS=5000`).

Works with Claude Code (Bash + PowerShell + Read + Write/Edit hooks),
Codex CLI (PostToolUse `decision: block`), and Cursor IDE (preToolUse
`updated_input` command rewrite; shell output only).

---

## How it works

thlibo is one binary plus PreToolUse hooks. Inference runs in a
separate sidecar daemon, [inferd](https://github.com/3rg0n/inferd),
which thlibo's installer detects-or-installs alongside itself.

```
Claude Code: about to run `git status`
      │
      ▼
 PreToolUse hook (bash / powershell)
      │  ask: thlibo rewrite "git status"
      ▼
 thlibo (CLI)
      │  registry lookup on argv[0] == "git"  →  wrap it
      │
      ▼
 hook emits: updatedInput = "thlibo exec -- git status"
      │
      ▼
 Claude Code runs the rewritten command
      │
      ▼
 thlibo exec runs the real git, captures stdout
      │
      ▼
 middleware: fast-path regex → git-filter (python script)
             OR router call → inferd sidecar → Gemma 4 processor
      │
      ▼
 compressed stdout, original stderr, original exit code
      │
      ▼
 Claude Code: tool_output = compressed bytes
      │
      ▼
 Model never sees the original — only the summary.
```

Script processors (`git-filter`, `npm-filter`, `cargo-filter`,
`pytest-filter`, `ndjson-filter`, `stacktrace-filter`,
`lint-filter`, `pdf-to-md`) are deterministic Python scripts — they
don't need inferd for their core path. (One exception: a *scanned*,
image-only PDF has no extractable text, so `thlibo case` hands its
pages to inferd's Gemma vision model for OCR — see below.) Prompt
processors (`compress`, `casefolder`, `shorthand`) dispatch through
inferd for LLM-driven summarisation of unfamiliar output.
`cordon-filter` (semantic anomaly surfacer) embeds windows via inferd
and surfaces the rare ones.

Everything runs on your machine. No network calls during inference,
no telemetry, nothing leaves localhost.

---

## Install

### One-liner (Unix)

```bash
curl -fsSL https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh | bash
```

Pin to a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh | THLIBO_VERSION=v0.7.7 bash
```

### One-liner (Windows PowerShell)

```powershell
irm https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.ps1 | iex
```

Or pinned:

```powershell
$env:THLIBO_VERSION='v0.7.7'; irm https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.ps1 | iex
```

Both installers:
1. Download the platform-matching release tarball/zip from GitHub.
2. Verify SHA-256 against `SHA256SUMS` published in the same release.
3. Extract `thlibo` into `~/.local/bin` (Unix) or
   `%LOCALAPPDATA%\thlibo\bin` (Windows).
4. On Windows, add the install dir to the User PATH (no admin).
5. Run `thlibo install` — writes Claude Code hooks, mirrors
   processors, probe-or-installs the inferd sidecar.

Skip step 5 with `THLIBO_SKIP_INSTALL=1` if you want to inspect the
binary before running configure.

### Prerequisites for running

- Python 3.8+ — the built-in script processors are Python.
- `jq` — the Claude Code hook shell script needs it. Install via
  your package manager or `winget install jqlang.jq` on Windows.
- `git` — for git-related compression you probably have it already.

On Windows you also need `bash` on PATH so Claude Code can execute
the PreToolUse hook script. **Git Bash (bundled with Git for
Windows) is sufficient** — if `git` works in your shell, you're
fine. WSL is also an option.

### From source

```bash
git clone https://github.com/3rg0n/thlibo.git
cd thlibo
mkdir -p bin

# Unix:
go build -o bin/thlibo ./cmd/thlibo

# Windows (note the .exe):
go build -o bin/thlibo.exe ./cmd/thlibo
```

Copy the binary somewhere on your `$PATH` (e.g. `~/.local/bin/`)
and run `thlibo install`.

### Run the installer

```bash
thlibo install
```

This does five things:

1. Mirrors the embedded built-in processors to `~/.thlibo/processors/`
   (script processors need an on-disk directory to chdir+exec into).
2. Writes the Claude Code hook scripts to `~/.thlibo/hooks/`.
3. Merges PreToolUse/Bash, PowerShell, Read, and Write/Edit hook
   entries into `~/.claude/settings.json` (preserves all your
   existing hooks and settings verbatim — idempotent).
4. **Probe-then-delegate inferd**: if inferd is already running,
   uses it; if installed but stopped, starts it; otherwise
   downloads the latest inferd release and runs inferd's bundled
   installer (which registers inferd's own per-platform autostart).
5. Reports the plan as it goes (use `--dry-run` to see without
   touching anything).

No admin/root required, on any OS — for thlibo *or* inferd. All three
inferd autostart mechanisms are per-user and need no elevation: a
launchd LaunchAgent (macOS), a `systemctl --user` unit (Linux), and a
Startup-folder shortcut (Windows). A fresh install goes from nothing to
a running, login-autostarting daemon with no manual step.

### Install flags

```
--dry-run             Report the plan, don't apply it.
--processors-dir      Override ~/.thlibo/processors.
--hook-dir            Override ~/.thlibo/hooks.
--settings            Override ~/.claude/settings.json.
--skip-hook           Mirror processors only; don't touch Claude Code.
--skip-inferd         Don't probe / install the inferd sidecar.
--inferd-version      Pin inferd to a specific tag (default: latest).
--codex               Also install the Codex CLI PostToolUse hook.
--cursor              Also install the Cursor IDE preToolUse hook.
```

With `--codex`, thlibo writes a PostToolUse hook to `~/.codex/hooks.json`
and sets `[features] hooks = true` in `~/.codex/config.toml`. Codex
gates command hooks behind a **trust step**: after install, run `/hooks`
inside Codex, review the thlibo hook, and approve it — until you do,
Codex sees the hook but won't run it (compression stays off). The
installer prints this reminder.

The model GGUF (~5.1 GB Gemma 4 E4B) is downloaded by inferd on
first inference request, into a shared per-platform model store
(`~/.local/share/models/` on Linux, `~/Library/Application Support/models/`
on macOS, `%LOCALAPPDATA%\models\` on Windows). thlibo doesn't
manage the model — that's inferd's job.

### Install footprint — what gets modified

`thlibo install` touches these paths and nothing else. Every hook and
skill file is SHA-stamped: if you've edited one, the new version lands
at `<path>.new` and your edit is preserved.

| Path | What |
|---|---|
| `~/.local/bin/thlibo` (Unix) · `%LOCALAPPDATA%\thlibo\bin\` (Win) | the `thlibo` binary (placed by the one-liner installer; on Windows the dir is added to the **User** PATH) |
| `~/.thlibo/hooks/` | the six PreToolUse hook scripts (Bash + PowerShell variants of exec / read / write) |
| `~/.thlibo/processors/` | the embedded built-in processors, mirrored to disk (your own processors here are left untouched) |
| `~/.claude/settings.json` | **only** the `hooks` block — PreToolUse matchers for `Bash`, `PowerShell`, `Read`, `Write`, `Edit` are merged in; every other key and hook you have is preserved verbatim |
| `~/.claude/skills/caselog/` | the `/caselog` skill |
| inferd binary + `backends/` libs | `~/.local/bin` (Unix) · `%LOCALAPPDATA%\inferd\` (Win), via inferd's installer |
| inferd autostart | LaunchAgent (macOS) · `systemctl --user` unit (Linux) · Startup-folder shortcut (Windows) |
| `~/.codex/hooks.json` + `config.toml` | **only** with `--codex` |

On macOS the one-liner installer (`install.sh`) also strips the
Gatekeeper quarantine attribute (`xattr -d com.apple.quarantine`) from
the downloaded `thlibo` binary so it runs without a "blocked" dialog.

`thlibo install` does **not** modify any other `settings.json` keys — in
particular it does not touch `skipDangerousModePermissionPrompt`,
`skipWebFetchPreflight`, or any permission/safety setting.

### Two behaviors worth knowing

These are intentional and named in [`THREAT_MODEL.md`](THREAT_MODEL.md)
(findings MA-2 and MA-6); calling them out here so they aren't a
surprise:

1. **The hooks auto-allow their own rewrites.** When a PreToolUse hook
   rewrites a command (or substitutes a compressed file for the Read
   tool), it emits `permissionDecision: "allow"` for that single,
   thlibo-rewritten invocation — so Claude Code doesn't re-prompt for
   the thing thlibo just produced. It only ever allows the rewritten
   form it emitted; it does not blanket-allow other commands. The
   rewritten command is visible to you and logged by `thlibo exec`.
2. **The PreToolUse hook is persistent.** It's a one-time install but
   the hook stays in `~/.claude/settings.json` and intercepts matching
   tool calls in **every future Claude Code session** until you run
   `thlibo uninstall`. `cat ~/.claude/settings.json` to see it.

---

## Uninstall

```bash
thlibo uninstall            # remove hooks + scripts; leave ~/.thlibo
thlibo uninstall --purge    # also delete ~/.thlibo (processors + state)
```

Inferd is left running because other tools may use it. To remove
inferd separately, use inferd's own uninstaller — see
[inferd's docs](https://github.com/3rg0n/inferd).

---

## Customise

### Drop in your own processor

```bash
mkdir -p ~/.thlibo/processors/my-tool
cat > ~/.thlibo/processors/my-tool/processor.yaml <<'YAML'
name: my-tool
type: script
entry: run.py
commands:
  - my-custom-cli
match: "^Running: "
description: >
  Compresses my-custom-cli's verbose progress output to a summary line.
YAML
cat > ~/.thlibo/processors/my-tool/run.py <<'PY'
import sys
for line in sys.stdin:
    if not line.startswith("Progress:"):
        sys.stdout.write(line)
PY
chmod +x ~/.thlibo/processors/my-tool/run.py
```

Restart your shell (or re-run `thlibo install`) and the hook picks
it up on the next Claude Code invocation. User processors with the
same name as a built-in override the built-in.

### Script processor vs prompt processor

| | Script | Prompt |
|---|---|---|
| Descriptor | `processor.yaml` + entry file | `processor.md` (YAML frontmatter + body) |
| Speed | ~10 ms | ~200-800 ms (inferd round-trip) |
| Determinism | Always the same output for the same input | Model-dependent |
| When to use | Fixed-format output (git, npm, cargo, known log schemas) | Unfamiliar output; stack traces; arbitrary logs |
| Inferd needed? | No | Yes |

### Built-in processors

| Name | Type | Handles |
|---|---|---|
| `git-filter` | script | `git status`, `git diff`, `git log` |
| `npm-filter` | script | `npm`, `npx`, `pnpm`, `yarn` |
| `cargo-filter` | script | `cargo build`, `cargo test`, `cargo clippy` |
| `pytest-filter` | script | `pytest` output |
| `ndjson-filter` | script | structured-log streams |
| `stacktrace-filter` | script | Python / Go / Rust / Java / Node stack traces |
| `lint-filter` | script | clang, gcc, clippy, eslint, golangci-lint, gosec, shellcheck, flake8, ruff, mypy, rubocop, stylelint. Auto-wraps `gosec`, `staticcheck`, `golangci-lint`, `shellcheck` (not `go`/`make`/`docker` — see below) |
| `go-test-filter` | script | `go test -v` / `go test -json` — keeps failures + package tally, drops passing-test noise. Auto-wraps `go test` (only that subcommand) |
| `pdf-to-md` | script | PDF → GitHub-flavored markdown (text + tables; scanned/image-only pages OCR'd via inferd Gemma vision) |
| `shorthand` | prompt | LLM-facing prose compression (SKILL.md, CLAUDE.md, system prompts) |
| `compress` | prompt | Generic verbose output, fallback |
| `casefolder` | prompt | Stack traces, error logs, crash output |

**`go` is matched per-subcommand.** `go test` wraps (→ `go-test-filter`),
but `go build` / `go run` / `go vet` / `go generate` do **not** — `go`'s
argv[0] is multiplexed, so a `command_prefixes: ["go test"]` rule wraps
exactly the test verb and leaves the others alone. **Intentionally not
wrapped at all** (a recorded decision): `make`, `docker build` — they
emit too many output shapes for a single filter to compress safely.

---

## Check it's working

```bash
# Inferd sidecar is running
# Linux:    systemctl --user is-active inferd
# macOS:    launchctl list | grep io.inferd.daemon
# Windows:  sc.exe query inferd-daemon

# Hook is registered in Claude Code
grep -c thlibo ~/.claude/settings.json
# Expected: 5+ (Bash + PowerShell + Read + Write + Edit matchers)

# Direct test of the rewrite path
thlibo rewrite 'git status'
# Expected stdout: "<thlibo-path> exec -- git status"
# Expected exit:   0

# Direct test of the exec path
thlibo exec -- git diff HEAD~5 | wc -c
# Expected: far fewer bytes than `git diff HEAD~5 | wc -c` alone.
```

If the hook silently doesn't fire in a Claude Code session, check
the debug log:

```bash
claude --debug-file /tmp/claude.log 'Run git status via Bash'
grep -E 'Hook|PreToolUse|updatedInput' /tmp/claude.log
```

You should see `Hook PreToolUse:Bash (PreToolUse) success:` with a
`updatedInput` object pointing at `thlibo exec -- ...`. If you don't,
the hook script isn't being invoked — usually a PATH issue (the hook
needs `thlibo` and `jq` on Claude Code's Bash PATH).

---

## Output streams

thlibo uses stdout and stderr separately and on purpose:

- **stdout** — only the compressed (or pass-through) bytes the AI
  client should consume. Always safe to capture.
- **stderr** — diagnostics: reduction summaries, fallback reasons
  ("backend unavailable; emitting original"), and the occasional
  background update-available banner.

Don't merge them with `2>&1` when feeding output to an AI client or
to `thlibo` itself. The update banner and other stderr lines are
human diagnostics, not data — merging them risks polluting the
captured payload. Examples:

```bash
# Good: only the compressed payload reaches the AI.
thlibo exec -- git diff HEAD~5 > diff.compressed

# Good: keep stderr visible for the human in the terminal,
# stdout clean for the pipe.
thlibo exec -- npm install | other-tool

# Avoid: merges human diagnostics into the data stream.
thlibo exec -- git diff 2>&1 | other-tool
```

If you must capture stderr for debugging, route it to its own file
(`2>thlibo.err`) instead of merging.

---

## Disable without uninstalling

Temporarily stop compressing without removing anything:

```bash
# Set this in your shell profile or Claude Code environment:
export THLIBO_DISABLED=1
```

Every hook honours this flag and exits passthrough immediately.

---

## Security model

- **All-local at runtime.** No network calls during inference. The
  inferd sidecar listens only on a Unix domain socket / Windows named
  pipe / loopback TCP — never on a public interface.
- **One network touch per host: model download.** Inferd fetches the
  GGUF on first request and verifies a SHA-256 published in inferd's
  own release. After that, the daemon is offline.
- **User-scoped.** On Unix, the inference socket is mode 0660 owned
  by group `inferd-users` (or user-only when the group doesn't
  exist); admin socket is 0600 owned by the daemon user. On Windows,
  the pipe ACL grants the current user only; Everyone is excluded.
- **No elevation, anywhere.** `thlibo install` runs entirely under your
  user account, and so does inferd's: all three autostart mechanisms are
  per-user — LaunchAgent (macOS), `systemctl --user` unit (Linux),
  Startup-folder shortcut (Windows). No admin/root at any point.
- **Fallback on every error.** If anything in the compression path
  fails — inferd unreachable, script crashes, processor times out,
  malformed response — the original output is returned unchanged.
  The AI never sees a broken intermediate state.
- **Hook auto-allows rewritten commands.** By design, the PreToolUse
  hook returns `permissionDecision: allow` for every Bash command it
  rewrites so users aren't re-prompted for the same action. That
  means: once installed, every Bash tool-call that matches the hook
  matcher runs through thlibo's rewrite without a Claude Code
  permission prompt. See [`THREAT_MODEL.md`](THREAT_MODEL.md) finding
  #15 for the trade-off discussion.
- **Activity log redaction.** `~/.thlibo/logs/*.ndjson` records
  byte-count telemetry only; subprocess stderr and error strings
  pass through a secret-pattern redactor before disk write (AWS keys,
  GitHub PATs, HuggingFace tokens, generic `SECRET=` / `API_KEY=`
  assignments). The redactor is a best-effort backstop, not a
  replacement for keeping secrets out of subprocess output.
- **Inferd version gate.** thlibo refuses to delegate to inferd
  binaries older than `MinInferdVersion` (currently v0.4.0 — the first
  release with the unified IPC wire thlibo's codec speaks; earlier
  daemons are unreachable). The gate detects an older daemon and
  triggers a fresh inferd install instead of failing open forever.
- **Supply chain.** Every GitHub Action in this repo is pinned by
  commit SHA. Every release archive, the `SHA256SUMS`, and the
  CycloneDX SBOM are signed with cosign via Sigstore's keyless flow
  — no key to manage, identity rooted in the GitHub OIDC token for
  `.github/workflows/release.yml` at the release tag, transparency-
  log entry published to `rekor.sigstore.dev`. Verification command
  is in the release notes for each tag. The release pipeline runs
  the installer scripts against the just-built archive on
  ubuntu-latest + windows-latest before `gh release create` — a
  broken installer cannot ship. A full threat model lives at
  [`THREAT_MODEL.md`](THREAT_MODEL.md).

---

## Known limitations

- **Bash, PowerShell, Read, and Write/Edit tool coverage; MCP tools
  bypass.** The PreToolUse hooks intercept Claude Code's `Bash` tool,
  `PowerShell` tool (when `CLAUDE_CODE_USE_POWERSHELL_TOOL=1`),
  `Read` tool (for files dragged into the window or referenced by
  path), and `Write` / `Edit` tools (when shorthand auto-apply is
  enabled). `Grep` / `Glob` / `MCP`-served tools bypass the hook —
  their inputs and outputs are not intercepted.
- **Cursor: shell commands only.** `thlibo install --cursor` installs a
  `preToolUse` hook that rewrites the Shell command (via `updated_input`)
  to run through `thlibo exec`, so terminal output is compressed before
  the model reads it — the same command-wrap mechanism as the Claude
  Code Bash hook. Cursor's hooks **cannot** substitute `Read` or MCP tool
  output for the built-in tools (`afterShellExecution` is observe-only;
  `updated_mcp_tool_output` is MCP-server-only), so on Cursor only shell
  output is compressed. User-level `~/.cursor/hooks.json` loads
  automatically; project-scoped hooks require a trusted workspace.
- **Compound shell commands pass through.** `git status | head` or
  `cmd1 && cmd2` are not rewritten — only single-program
  invocations. `thlibo rewrite` matches on `argv[0]` and deliberately
  doesn't try to parse a shell AST.
- **Inferd is a separate dependency.** thlibo no longer ships its
  own inference daemon. The first `thlibo install` on a fresh host
  pulls inferd over HTTPS and runs inferd's installer; if you need
  air-gapped install, fetch inferd manually first (see
  [github.com/3rg0n/inferd](https://github.com/3rg0n/inferd)) and
  thlibo's probe-then-delegate will use it without touching the
  network.

---

## Development

- AI-assistant guidance: [`CLAUDE.md`](CLAUDE.md).
- Architecture decisions: [`docs/adr/`](docs/adr/).
- Changelog: [`CHANGELOG.md`](CHANGELOG.md).
- Run the tests: `go test ./... -timeout 120s`
- Scanner sweep: `go vet ./... && staticcheck ./... && gosec ./... && govulncheck ./...`

### Project layout

```
cmd/
  thlibo/             User CLI: rewrite, exec, compress, case, install,
                      uninstall, shorthand, version.
internal/
  adapters/
    claudecode/       PreToolUse hooks (Bash + PowerShell + Read + Write/Edit),
                      /caselog skill, settings.json merger.
    codex/            PostToolUse hook (decision: block) + hooks.json merger.
  casefile/           `thlibo case` directory builder (compressed.log + summary + meta).
  config/             ~/.thlibo/config.yaml read/write.
  execpolicy/         `thlibo exec` allow/deny policy.
  inferd/             thlibo's own codec for inferd's v2 IPC wire
                      (length-prefixed framing); no client dependency.
  install/            Disk mirror + per-platform inferd sidecar installer
                      (probe-then-delegate) + v0.5 → v0.6 migration.
  logx/               NDJSON activity log with rolling rotation + secret redactor.
  middleware/         Main flow: short-circuit → fast-path → router → chain.
  processors/         Registry, descriptors, script+prompt dispatch, thought-stripping.
  promptsan/          Gemma marker sanitiser for untrusted tool output.
  router/             Processor routing via inferd response_format (JSON-Schema).
  shellcmd/           Minimal shell-command argv[0] extractor.
  shorthand/          LLM-facing prose compression (SKILL.md / CLAUDE.md).
  update/             Background release check + upgrade banner.
  version/            Build-tag constant (overridable via -ldflags).
processors/           Embedded built-ins (go:embed).
skills/               Claude Code skills: /caselog.
```

---

## Why "thlibo"?

The Greek word θλίβω means *to press, squeeze, compress*. Same root
as "tribulation" — being crushed down. Thlibo crushes tool output
before the model ever sees it.
