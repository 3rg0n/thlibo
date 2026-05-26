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

Works with Claude Code (Bash + PowerShell + Read + Write/Edit hooks)
and Codex CLI (PostToolUse `decision: block`).

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
`pdf-to-md`) are deterministic Python scripts — they don't need
inferd. Prompt processors (`compress`, `casefolder`, `shorthand`)
dispatch through inferd for LLM-driven summarisation of unfamiliar
output.

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
curl -fsSL https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh | THLIBO_VERSION=v0.7.0 bash
```

### One-liner (Windows PowerShell)

```powershell
irm https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.ps1 | iex
```

Or pinned:

```powershell
$env:THLIBO_VERSION='v0.7.0'; irm https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.ps1 | iex
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

No admin/root required for thlibo's own install. Inferd's Windows
service registration *does* need admin (the installer surfaces a
clear instruction when that's the case); systemd / launchd setups
are per-user and need no elevation.

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
```

The model GGUF (~5.1 GB Gemma 4 E4B) is downloaded by inferd on
first inference request, into a shared per-platform model store
(`~/.local/share/models/` on Linux, `~/Library/Application Support/models/`
on macOS, `%LOCALAPPDATA%\models\` on Windows). thlibo doesn't
manage the model — that's inferd's job.

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
| `pdf-to-md` | script | PDF → GitHub-flavored markdown (text + tables; multimodal pages in v0.8) |
| `shorthand` | prompt | LLM-facing prose compression (SKILL.md, CLAUDE.md, system prompts) |
| `compress` | prompt | Generic verbose output, fallback |
| `casefolder` | prompt | Stack traces, error logs, crash output |

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
- **No elevation for thlibo itself.** `thlibo install` runs entirely
  under your user account. Inferd's Windows service registration
  needs admin (one-time, surfaced clearly); systemd / launchd setups
  are per-user.
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
  binaries older than `MinInferdVersion` (currently v0.1.14). Older
  versions had a tempdir-copy bug that crashed on /tmp-constrained
  hosts (WSL) and a macOS launchagent that registered a mock daemon
  silently. The gate detects them and triggers a fresh inferd
  install instead of using a known-bad version.
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
- **No Cursor support.** Claude Code and Codex CLI are both
  supported; Cursor is not.
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
  inferdcli/          Thin wrapper over inferd's Go client.
  install/            Disk mirror + per-platform inferd sidecar installer
                      (probe-then-delegate) + v0.5 → v0.6 migration.
  logx/               NDJSON activity log with rolling rotation + secret redactor.
  middleware/         Main flow: short-circuit → fast-path → router → chain.
  processors/         Registry, descriptors, script+prompt dispatch, thought-stripping.
  promptsan/          Gemma marker sanitiser for untrusted tool output.
  router/             Gemma native tool-call routing + GBNF grammar.
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
