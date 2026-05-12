# Thlibo

> From Greek θλίβω — to press, crush, compress.
> Programmatic inference daemon + AI client middleware.
> One warm model. Local IPC. No user-facing platform.

---

## What it is

Two binaries with a strict separation of concerns:

| Binary | Role | Knows about |
|---|---|---|
| `thlibod` | Inference daemon. Loads Gemma 4 E4B once, stays warm, accepts requests via IPC. | llamafile, inference params, socket permissions. Nothing else. |
| `thlibo` | Middleware. Hooks into AI clients, routes tool output through processors, posts to daemon. | Processors, use cases, client hooks, routing logic. |

The daemon has no knowledge of processors, Claude, hooks, or use cases.
thlibo middleware has no knowledge of model loading, llamafile, or inference.

---

## Architecture

```
machine boots
      │
      ▼
  thlibod  (launchd / systemd)
      ├─ acquire lock — one instance only, never restarts model
      ├─ spawn llamafile as private child process
      ├─ wait until model ready
      ├─ open IPC sockets
      └─ accept requests indefinitely — model never unloads

      ─────────────────────────────────────────────────────

Claude Code / Codex / other AI client
      │
      │  PostToolUse hook (or equivalent)
      ▼
  thlibo  (middleware)
      │
      ├─ output < 2000 chars? ──────────────────► pass through
      │
      ├─ scan ~/.thlibo/processors/ on startup
      │   build processor registry from descriptors
      │
      ├─ fast-path: does any processor.yaml/md
      │   match: regex hit this output?
      │      └─ yes ──► dispatch directly, skip routing call
      │
      ├─ routing call to daemon (small prompt, no stream)
      │   "I have this output. Available processors: [list
      │    with descriptions]. Which should I run, or none?"
      │         │
      │         ├─ "git-filter" ──► run run.py, done
      │         ├─ "compress"   ──► call daemon with prompt, done
      │         ├─ "casefolder" ──► call daemon with prompt, done
      │         ├─ "git-filter,compress" ──► chain them, done
      │         └─ "none"       ──► pass through
      │
      └─ final output → AI client
```

---

## Processors

Processors live in `~/.thlibo/processors/`. Each processor is a subfolder.
thlibo scans this directory at startup and builds the routing registry.

### Two processor types

**Script processor** — deterministic. Fast, lossless, language-agnostic.
thlibo pipes tool output through the entry file, reads result from stdout.

```
~/.thlibo/processors/
  git-filter/
    processor.yaml
    run.py          ← or run.sh / run.exe / run.bin
```

```yaml
# processor.yaml
name: git-filter
type: script
entry: run.py
match: "^(On branch|HEAD detached|Changes|Untracked)"
description: >
  Compresses git status, diff, and log output.
  Keeps changed files, branch, status. Drops noise.
```

**Prompt processor** — model-based. Single file. Frontmatter is config,
body is the system prompt sent to the daemon.

```
~/.thlibo/processors/
  casefolder/
    processor.md
```

```markdown
---
name: casefolder
type: prompt
thinking: true
think_briefly: false
temperature: 1.0
max_tokens: 1000
match: "(?i)traceback|error:|exception|fatal"
description: >
  Structures stack traces, error logs, and crash
  output into a diagnostic case folder.
---

<|think|>You are a diagnostic log analyst for an AI coding assistant.
Produce a structured case folder from raw log, trace, or error output.

Output format:
ERROR_TYPE: <category>
LOCATION: <file:line or service:endpoint>
MESSAGE: <verbatim error message, max 2 lines>
CONTEXT: <3-5 bullet points>
PATTERN: <new | recurring>

Rules:
- Strip all repeated identical lines
- One case folder per distinct error
- Output only the case folder(s). No preamble.
```

### Descriptor rules

```
processor.yaml present → script processor, entry field required
processor.md present   → prompt processor, body is system prompt
both present           → yaml wins, md body used as system prompt
neither present        → folder ignored
```

### Entry file execution

| Extension | Invoked as |
|---|---|
| `.py` | `python3 run.py` |
| `.sh` | `bash run.sh` |
| `.exe` / `.bin` | direct exec |

Input piped to stdin. Output read from stdout. Non-zero exit = fallback to original.

### Built-in processors (ship with thlibo)

These ship embedded in the thlibo binary as defaults. User processors in
`~/.thlibo/processors/` with the same name override built-ins.

```
compress/         prompt — general purpose output compressor
casefolder/       prompt — stack trace and log structurer
git-filter/       script — git status, diff, log
npm-filter/       script — npm list, install, audit
cargo-filter/     script — cargo test, build, clippy
```

### Full processors folder example

```
~/.thlibo/processors/
  git-filter/
    processor.yaml
    run.py
  npm-filter/
    processor.yaml
    run.py
  casefolder/
    processor.md
  compress/
    processor.md
  my-custom-processor/
    processor.md        ← drop in, picked up on next thlibo start
```

---

## Routing

thlibo asks the daemon which processor to run. Small prompt, no streaming,
just a processor name back. Cheap call.

Routing prompt is built dynamically from loaded processor descriptions:

```
system: "You are a processor router. Given tool output, return which
         processor to run from the list. Return name only, or 'none'.
         You may return two names separated by comma to chain them."

user:   "Available processors:
         - git-filter: Compresses git status, diff, log output...
         - npm-filter: Strips npm list, install, audit output...
         - casefolder: Structures stack traces and error logs...
         - compress: General purpose compressor for any output...

         Input (first 200 chars):
         On branch main
         Your branch is up to date with 'origin/main'.
         Changes not staged for commit:
         ..."
```

**Routing decision flow:**

```
1. fast-path: check match regex in each processor.yaml / processor.md
   → hit: dispatch immediately, no daemon routing call

2. routing call: ask daemon with truncated input + processor list
   → returns processor name(s) or "none"

3. dispatch:
   script processor  → pipe through entry file
   prompt processor  → build request, call daemon
   chain (a,b)       → run a, pipe result into b
   none              → pass through
```

---

## thlibod — inference daemon

### Responsibility

Loads Gemma 4 E4B once. Stays running. Accepts fully-formed inference
requests. Returns tokens. Nothing else.

No processor knowledge. No routing logic. No system prompt opinions.
thlibo middleware builds the complete request before it arrives.

### Request protocol

Newline-delimited JSON over Unix socket.

**Request:**
```json
{
  "id":                 "req-7f3a",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user",   "content": "..."}
  ],
  "temperature":        1.0,
  "top_p":              0.95,
  "top_k":              64,
  "max_tokens":         1000,
  "stream":             true,
  "image_token_budget": 280,
  "grammar":            ""
}
```

`id` is a client-generated string, echoed on every response frame for that
request. Enables cancellation, queue accounting, and multi-client debugging.
If omitted, daemon assigns an opaque ID and includes it on the first frame.

Defaults if omitted: `temperature=1.0`, `top_p=0.95`, `top_k=64` (Gemma 4
recommended). `image_token_budget` for multimodal only: `70|140|280|560|1120`.

`grammar` is a GBNF (llama.cpp grammar) string that constrains the
model's output token-by-token. Used by the router for structured
output (analogous to the Anthropic API's tool_use `input_schema`).
Empty string = unconstrained. The daemon forwards this verbatim to
llamafile and never interprets it.

**Response:** (every frame carries the request `id`)
```json
{"id": "req-7f3a", "type": "status", "status": "loading_model"}
{"id": "req-7f3a", "type": "status", "status": "ready"}
{"id": "req-7f3a", "type": "token",  "content": "..."}
{"id": "req-7f3a", "type": "done",   "usage": {"prompt_tokens": 412, "completion_tokens": 38}}
{"id": "req-7f3a", "type": "error",  "message": "queue full"}
```

Admin socket `status` frames use `id: "admin"`.

IPC endpoint not created until `{"status":"ready"}`.

### Concurrency

```
1 active generation at a time
10 queued requests (configurable)
client disconnect = job cancelled
queue full = immediate error, never blocks
```

### IPC endpoints

```
/run/thlibo/infer.sock    inference     group: thlibo-users    mode: 0660
/run/thlibo/admin.sock    admin/status  group: thlibo-admin    mode: 0600
```

| Platform | Path | Type |
|---|---|---|
| Linux    | `/run/thlibo/infer.sock`      | Unix domain socket |
| macOS    | `/var/run/thlibo/infer.sock`  | Unix domain socket |
| Windows  | `\\.\pipe\thlibo-infer`       | Named pipe (not Everyone) |
| Fallback | `127.0.0.1:47320`             | TCP, loopback only |

### Daemon lifecycle

```
1.  acquire lock (/run/thlibo/thlibod.lock) — exit if already locked
2.  spawn llamafile as private child on stdio or localhost port
3.  poll until model ready
4.  create IPC sockets with correct ownership and permissions
5.  emit {"status":"ready"}
6.  enter accept loop
7.  queue requests, 1 active at a time, stream tokens back
8.  llamafile crash → restart (max 3x), error on admin socket if exhausted
9.  SIGTERM → drain queue, shutdown llamafile, release lock, exit
```

---

## thlibo — middleware

### Responsibility

Hooks into AI clients. Scans processors. Routes tool output. Builds
fully-framed requests. Posts to daemon. Returns result to client.
Falls back to original output on any error path.

### Client adapters

| Client | Hook mechanism |
|---|---|
| Claude Code | PostToolUse in `~/.claude/settings.json` |
| Codex | Equivalent hook config |
| Proxy mode | `ANTHROPIC_BASE_URL=http://localhost:47321` — intercepts Read/Glob/Grep |

### Main flow

```go
func main() {
    input := readStdin()

    if len(input) < 2000 {
        fmt.Print(input)
        return
    }

    registry := loadProcessors("~/.thlibo/processors/")

    // fast-path: match regex
    if p := registry.Match(input); p != nil {
        fmt.Print(p.Run(input))
        return
    }

    // routing call
    names := routingCall(input, registry)
    if names == nil {
        fmt.Print(input)
        return
    }

    result := input
    for _, name := range names {
        p := registry.Get(name)
        result = p.Run(result)
    }
    fmt.Print(result)
}
```

Error on any step → fallback to original. Never breaks the AI client.

---

## Thinking mode

Owned entirely by the processor's `system.txt` or `processor.md` body.
thlibo passes the system prompt as-is. The daemon passes it to llamafile.
The GGUF chat template handles `<|think|>` token injection.

The daemon has no `thinking` toggle. Caller owns the prompt.

| Processor | Prompt prefix | Latency (est.) |
|---|---|---|
| compress | _(none)_ | ~200ms |
| casefolder | `<|think|>` | ~800ms |
| casefolder (reduced) | `<|think|>Think briefly.` | ~650ms |
| routing call | _(none)_ | ~100ms |

---

## Gemma 4 E4B reference

| Property | Value |
|---|---|
| Effective parameters | 4.5B |
| Context window | 128K tokens |
| Modalities | Text, Image, Audio |
| Recommended temperature | 1.0 |
| Recommended top_p | 0.95 |
| Recommended top_k | 64 |
| Audio max length | 30 seconds |
| Image token budgets | 70, 140, 280, 560, 1120 |

Image content must appear before text in multimodal prompts.
GGUF chat template handles all special token formatting.
Single-turn requests only in thlibo — multi-turn thought stripping not needed.

---

## Inference engine

Do not build this. Use llamafile (Mozilla).
Single self-contained executable. No install. No build toolchain.
thlibod spawns it as a private child. Never exposed externally.

**Model:** `bartowski/gemma-4-E4B-IT-GGUF` on HuggingFace.
`Q8_0` for quality on GPU. `Q4_K_M` for CPU-only (~2.5GB RAM).

---

## Security

| Threat | Mitigation |
|---|---|
| Unauthorised inference | Socket gated by `thlibo-users` group, mode 0660 |
| System prompt leakage | Prompts travel only from middleware to daemon, not on infer socket |
| Prompt injection | Processors handle text tasks only — no tool access in v0.1 |
| Queue flooding | Depth cap, queue full returned immediately |
| Multiple daemon instances | Lock file |
| llamafile crash | Monitor and restart (max 3x) |
| Open TCP port | Unix socket by default, TCP only as explicit fallback |

---

## Install flow

```bash
# 1. Download
curl -L .../thlibo        -o /usr/local/bin/thlibo        && chmod +x $_
curl -L .../thlibod       -o /usr/local/bin/thlibod       && chmod +x $_
curl -L .../llamafile     -o /usr/local/bin/thlibo-engine && chmod +x $_

# 2. Model
thlibo pull gemma-4-e4b
# downloads Q4_K_M GGUF to ~/.thlibo/models/

# 3. Install
sudo thlibo install
# creates thlibo-users group, adds current user
# creates /run/thlibo/ with correct permissions
# writes launchd plist (macOS) or systemd unit (Linux)
# starts thlibod, waits for ready
# writes PostToolUse hook to ~/.claude/settings.json
# creates ~/.thlibo/processors/ with built-in processors
```

---

## Directory layout

```
~/.thlibo/
  models/
    gemma-4-e4b-q4_k_m.gguf
  processors/
    git-filter/
      processor.yaml
      run.py
    npm-filter/
      processor.yaml
      run.py
    casefolder/
      processor.md
    compress/
      processor.md
    <your-custom-processor>/
      processor.md        ← drop in, picked up on next start
```

---

## Token savings estimate

| Tool output | Raw (est.) | After thlibo | Savings |
|---|---|---|---|
| `npm list` 200 deps | ~800 tokens | ~80 | 90% |
| Stack trace | ~400 tokens | ~120 | 70% |
| `git status` 50 files | ~300 tokens | ~60 | 80% |
| Test run, 2 failures | ~600 tokens | ~80 | 87% |
| Log file 500 lines | ~2000 tokens | ~150 | 92% |
| Short result (<500 tok) | pass-through | pass-through | — |

---

## v0.1 build checklist

**thlibod**
- [ ] llamafile child spawn, private port or stdio
- [ ] Lock file — single instance enforcement
- [ ] IPC sockets with correct group ownership and permissions
- [ ] Newline-delimited JSON protocol
- [ ] Queue: 1 active + 10 waiting, queue-full error, disconnect cancels
- [ ] Health: `loading_model` → `ready`
- [ ] llamafile crash detection and restart (max 3 attempts)
- [ ] Graceful shutdown: drain, stop child, release lock

**thlibo**
- [ ] Processor scanner — load `~/.thlibo/processors/` at startup
- [ ] Descriptor parser — `processor.yaml` and `processor.md` with frontmatter
- [ ] Fast-path match regex check before routing call
- [ ] Routing call to daemon — build prompt from registry descriptions
- [ ] Script processor dispatch — stdin/stdout, language detection
- [ ] Prompt processor dispatch — build request from md body + frontmatter vars
- [ ] Processor chaining
- [ ] Fallback to original on every error path
- [ ] Claude Code adapter (PostToolUse hook)
- [ ] Codex adapter
- [ ] Proxy mode (loopback HTTP for Read/Glob/Grep intercept)
- [ ] `thlibo install` — group, service, socket dir, hook config, processors dir
- [ ] `thlibo pull` — GGUF download with progress

---

## Repo structure

```
thlibo/
├── cmd/
│   ├── thlibod/              # daemon binary
│   └── thlibo/               # middleware binary
├── internal/
│   ├── daemon/               # lifecycle, lock, child process, restart
│   ├── ipc/                  # socket creation, permissions, JSON protocol
│   ├── processors/           # scanner, descriptor parser, dispatcher
│   │   ├── registry.go       # load and index processors
│   │   ├── script.go         # script processor runner
│   │   └── prompt.go         # prompt processor builder
│   ├── router/               # routing call logic, prompt builder
│   ├── queue/                # concurrency control, cancellation
│   ├── adapters/
│   │   ├── claudecode/       # PostToolUse hook adapter
│   │   ├── codex/            # Codex hook adapter
│   │   └── proxy/            # HTTP proxy for built-in tool intercept
│   └── install/              # group creation, service registration, dirs
└── processors/               # built-in processors (embedded at build time)
    ├── compress/
    │   └── processor.md
    ├── casefolder/
    │   └── processor.md
    ├── git-filter/
    │   ├── processor.yaml
    │   └── run.py
    └── npm-filter/
        ├── processor.yaml
        └── run.py
```

---

*Thlibo — θλίβω — to press, crush, compress*
*v0.1 spec — May 2026*
