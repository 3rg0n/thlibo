# thlibo

> From Greek *θλίβω* — to press, crush, compress.

**AI tool output compressor for local coding agents.** When Claude
Code runs `git status`, `npm install`, `cargo test`, or a verbose
log dump, thlibo intercepts the Bash-tool invocation, runs the real
command, and hands the AI a compressed version of the output —
preserving every load-bearing detail and dropping the noise.

Saves 60-99% of tokens on common dev commands without the AI
knowing the difference.

```
 git diff HEAD~5        68821 bytes      ──thlibo──►    929 bytes     (98.6%)
 npm list 200 deps      ~6200 bytes      ──thlibo──►   ~600 bytes     (90%)
 cargo test failing     ~4800 bytes      ──thlibo──►   ~480 bytes     (90%)
 log file 500 lines    ~20000 bytes      ──thlibo──►  ~1500 bytes     (92%)
```

Works with Claude Code today. Extends to other AI coding agents
through the same PreToolUse+rewrite mechanism RTK pioneered.

---

## How it works

thlibo is two binaries plus a PreToolUse hook:

```
Claude Code: about to run `git status`
      │
      ▼
 PreToolUse hook (bash script)
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
             OR router call → thlibod daemon → Gemma 4 processor
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

The daemon (`thlibod`) keeps a local Gemma 4 E4B model warm in the
background via llamafile. Script processors (`git-filter`, `npm-filter`,
`cargo-filter`) are deterministic Python scripts — they don't need
the daemon. Prompt processors (`compress`, `casefolder`) dispatch
through the daemon for LLM-driven summarization of unfamiliar output.

Everything runs on your machine. No network calls, no telemetry,
nothing leaves localhost.

---

## Install

v0.1 is pre-release. There is no binary distribution yet — you build
from source.

### Prerequisites

- Go 1.22+
- Python 3.8+ (for script processors)
- `jq` (for the Claude Code hook script)
- `git` (obviously)

On Windows, Git Bash or WSL is required — the PreToolUse hook is a
bash script.

### From source

```bash
git clone https://github.com/3rg0n/thlibo.git
cd thlibo
mkdir -p bin

# Unix:
go build -o bin/thlibo   ./cmd/thlibo
go build -o bin/thlibod  ./cmd/thlibod

# Windows (note the .exe):
go build -o bin/thlibo.exe   ./cmd/thlibo
go build -o bin/thlibod.exe  ./cmd/thlibod
```

Leave them in `./bin/` and pass `--daemon-path` to the installer,
or copy them somewhere already on your `$PATH` (e.g. `~/.local/bin/`
or `~/bin/`) and omit the flag.

### Run the installer

```bash
./bin/thlibo install
# Or include the model download in one step:
./bin/thlibo install --pull-model --allow-unpinned
```

This does five things:

1. Mirrors the embedded built-in processors to `~/.thlibo/processors/`
   (script processors need an on-disk directory to chdir+exec into).
2. Writes the Claude Code hook script to `~/.thlibo/hooks/thlibo-rewrite.sh`.
3. Merges a PreToolUse/Bash hook entry into `~/.claude/settings.json`
   (preserves all your existing hooks and settings verbatim — idempotent).
4. Registers `thlibod` for per-user autostart:
   - **Windows:** `.cmd` shim in `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\`
   - **macOS:** `~/Library/LaunchAgents/cisco.thlibo.daemon.plist`, loaded via `launchctl`
   - **Linux:** `~/.config/systemd/user/cisco.thlibo.daemon.service`, enabled via `systemctl --user`
5. Reports the plan before applying (use `--dry-run` to see without touching anything).

No admin/root required. No password prompts. Everything lives under
your user's home directory.

### Install flags

```
--dry-run           Report the plan, don't apply it.
--processors-dir    Override ~/.thlibo/processors.
--hook-dir          Override ~/.thlibo/hooks.
--settings          Override ~/.claude/settings.json.
--skip-hook         Mirror processors only; don't touch Claude Code.
--skip-autostart    Don't register the daemon for autostart.
--daemon-path       Absolute path to thlibod (default: alongside thlibo).
--engine-path       Path to llamafile/engine binary (passed to thlibod -engine).
--pull-model        Download the default GGUF (~2.5 GB) as part of install.
--allow-unpinned    Allow --pull-model before the canonical SHA is pinned
                    (required in v0.1; goes away once a release hash is
                    recorded in internal/install/model.go).
```

### Download the model separately

If you skip `--pull-model` at install time, pull it later with:

```bash
./bin/thlibo pull gemma-4-e4b
```

Writes the GGUF to `~/.thlibo/models/` (or `$THLIBO_MODELS_DIR`),
resumes from any partial `.part` file, and verifies SHA-256 against
the hash pinned at build time. Progress prints on stderr so stdout
remains clean for scripting.

**No HuggingFace account required.** The default model
(`unsloth/gemma-4-E4B-it-GGUF`, Apache-2.0) is a public,
imatrix-calibrated repack — downloads work anonymously. 1.2M+
downloads at the time of pinning; thlibo pins a specific repo
revision so the file bytes are stable across upstream reuploads.
If you need a different quantisation from Google's gated
`google/gemma-4-E4B-it` repo, that's where you'd need an HF
account + token; v0.1 uses the unsloth pin so you don't.

### Start the daemon now (skip autostart wait)

```bash
./bin/thlibod
```

The daemon runs in the foreground with logging. When you're happy it
works, autostart will handle it at every subsequent logon.

---

## Uninstall

There's no `thlibo uninstall` subcommand yet (v0.2). To remove by hand:

```bash
# Stop the daemon
# Windows:  taskkill /F /IM thlibod.exe
# macOS:    launchctl bootout gui/$UID ~/Library/LaunchAgents/cisco.thlibo.daemon.plist
# Linux:    systemctl --user disable --now cisco.thlibo.daemon.service

# Remove the autostart entry
# Windows:  del "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\cisco.thlibo.daemon.cmd"
# macOS:    rm ~/Library/LaunchAgents/cisco.thlibo.daemon.plist
# Linux:    rm ~/.config/systemd/user/cisco.thlibo.daemon.service

# Remove the Claude Code hook entry
# Edit ~/.claude/settings.json and delete the PreToolUse group with
# matcher "Bash" whose command ends in thlibo-rewrite.sh.
# (Other hooks in that file survive.)

# Remove thlibo's own files
rm -rf ~/.thlibo
```

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
| Speed | ~10 ms | ~200-800 ms (daemon call) |
| Determinism | Always the same output for the same input | Model-dependent |
| When to use | Fixed-format output (git, npm, cargo, known log schemas) | Unfamiliar output; stack traces; arbitrary logs |
| Daemon needed? | No | Yes |

### Built-in processors

| Name | Type | Handles |
|---|---|---|
| `git-filter` | script | `git status`, `git diff`, `git log` |
| `npm-filter` | script | `npm`, `npx`, `pnpm`, `yarn` |
| `cargo-filter` | script | `cargo build`, `cargo test`, `cargo clippy` |
| `compress` | prompt | Generic verbose output, fallback |
| `casefolder` | prompt | Stack traces, error logs, crash output |

---

## Check it's working

```bash
# Daemon is running and listening
# Windows:  tasklist | findstr thlibod
# Unix:     pgrep thlibod

# Hook is registered in Claude Code
cat ~/.claude/settings.json | grep -A3 thlibo-rewrite

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

## Disable without uninstalling

Temporarily stop compressing without removing anything:

```bash
# Set this in your shell profile or Claude Code environment:
export THLIBO_DISABLED=1
```

(v0.2: the hook honours this flag and exits passthrough immediately.
Not implemented in v0.1; for now, remove the hook entry from
`~/.claude/settings.json` to disable.)

---

## Security model

- **All-local.** No network calls. The daemon listens only on a Unix
  domain socket / Windows named pipe / loopback TCP — never on a
  public interface.
- **User-scoped.** On Unix, the inference socket is mode 0660 owned
  by group `thlibo-users`; admin socket is 0600 owned by the daemon
  user. On Windows, the pipe ACL grants the current user only;
  Everyone is excluded.
- **No elevation.** `thlibo install` runs entirely under your user
  account. No root / admin / sudo / UAC prompts.
- **Fallback on every error.** If anything in the compression path
  fails — daemon down, script crashes, processor times out,
  malformed response — the original output is returned unchanged.
  The AI never sees a broken intermediate state.
- **Model stays offline.** The GGUF lives at `~/.thlibo/models/`;
  `thlibod` spawns `llamafile` as a private child on stdio.
  llamafile is never exposed on the network.

---

## Known limitations (v0.1)

- **Bash tool only.** The PreToolUse+rewrite mechanism only affects
  the Bash tool. `Read`, `Grep`, `Glob`, and MCP tools bypass this
  path. v0.2 may add an HTTP proxy mode for universal coverage.
- **No GGUF bundling yet.** The installer doesn't download the
  Gemma 4 model automatically — you need to fetch
  `bartowski/gemma-4-E4B-IT-GGUF` from HuggingFace manually and
  place it at `~/.thlibo/models/gemma-4-e4b-q4_k_m.gguf`. v0.1
  release will bundle the model in the release archive.
- **No Codex or Cursor support.** The adapters package has
  scaffolding for both but v0.1 only ships the Claude Code hook.
- **Compound shell commands pass through.** `git status | head` or
  `cmd1 && cmd2` are not rewritten. Only single-program invocations
  are wrapped.

---

## Uninstalling the model

Models can be 2-8 GB. To clean them up separately from the rest of
thlibo:

```bash
rm ~/.thlibo/models/gemma-4-e4b-q4_k_m.gguf
```

The daemon will report an engine spawn failure on next start, which
the middleware catches via the restart-cap mechanism (A11) and
eventually gives up. Installing a fresh model resumes normal operation.

---

## Development

- Spec: [`.plan/thlibo-spec.md`](.plan/thlibo-spec.md) is the source
  of truth. Release gate at [`.plan/release-gate.md`](.plan/release-gate.md)
  lists every requirement with pass conditions.
- AI-assistant guidance: [`CLAUDE.md`](CLAUDE.md).
- Changelog: [`CHANGELOG.md`](CHANGELOG.md).
- Run the tests: `go test ./... -timeout 120s`
- Scanner sweep: `go vet ./... && staticcheck ./... && gosec ./... && govulncheck ./...`

### Project layout

```
cmd/
  thlibo/          User CLI: rewrite, exec, install.
  thlibod/         Inference daemon.
internal/
  daemon/          Lifecycle, lock, engine supervisor, queue.
  ipc/             JSON protocol over sockets/pipes.
  processors/      Registry, descriptors, script+prompt dispatch, thought-stripping.
  router/          Gemma native tool-call routing + GBNF grammar.
  middleware/      Main flow: short-circuit → fast-path → router → chain.
  adapters/
    claudecode/    PreToolUse hook + settings.json merger.
    codex/         (v0.2 placeholder.)
  install/         Disk mirror + per-user autostart (Windows/macOS/Linux).
  shellcmd/        Minimal shell-command argv[0] extractor.
  queue/           Single-active admission queue.
processors/        Embedded built-ins (go:embed).
```

---

## Why "thlibo"?

The Greek word θλίβω means *to press, squeeze, compress*. Same root
as "tribulation" — being crushed down. Thlibo crushes tool output
before the model ever sees it.
