# MAESTRO Threat Model — thlibo v0.1.0

- **Project**: thlibo (`github.com/3rg0n/thlibo`)
- **Date**: 2026-05-14
- **Framework**: MAESTRO (OWASP MAS v1.0 + CSA MAESTRO Feb-2025) with OWASP ASI Threat Taxonomy (T1–T47, BV-1–BV-12)
- **Scope**: HEAD commit (`main`, post-0.1.0 release) — 104 tracked files, Go 1.26.3

## Executive Summary

Nine MAESTRO agents analysed 91 source and config files across the seven architectural layers, plus a dependency CVE scan (`govulncheck`) and an agent/plug-in integrity audit of the embedded processors and hook shell scripts. The analysis surfaced **37 raw findings**; after deterministic normalisation, VCS-claim verification, and deduplication by `(file, line, threat_id)`, the merged set is **28 findings** — **0 critical, 2 high, 11 medium, 13 low, 2 info**.

**Current status (post-hardening, 2026-05-14): 0 open, 0 Accepted.** All 28 findings are **Mitigated** by code fixes. The four items (#16, #17, #22, #24) originally marked Accepted-by-design were subsequently implemented as defence-in-depth once the `inferd` split surfaced a multi-tenant story where the single-user deployment premise no longer holds.

The single most important finding is **L1/INTEGRITY shared**: tool-output bytes from arbitrary CLI commands (`git`, `npm`, `cargo`, any other Bash-matched tool) are concatenated into prompts for the local Gemma 4 model and for the router **without any token-level sanitisation**. Gemma's native tool-call markers (`<|tool_call|>`, `<tool_call|>`) appearing in the raw `git diff` or `npm` output can influence router decisions and prompt-processor output. Impact is bounded by the fallback-on-error contract (a bad routing decision falls back to the original bytes, so the AI client never *breaks*), but a confused router can silently apply the wrong processor chain.

The second-tier finding cluster is **supply-chain hardening gaps**: GitHub Actions in `release.yml` / `ci.yml` / `pages.yml` are pinned by *semver tag*, not by commit SHA, so a hijacked action version could reach the release pipeline. The one-line `curl | bash` / `iwr | iex` installers are functional and verify SHA256SUMS against the release, but the SHA256SUMS itself is hosted on the same release, so a release-level compromise defeats the checksum.

Every finding below lives inside the **trust-boundary invariants already documented in the ADRs** — nothing found requires reopening the single-warm-model / per-user-autostart / no-shim decisions. The mitigations for the two High findings are code-local.

**Validation manifest**: `agents_run=9, raw_findings=37, normalized=37, repaired=0, dropped_irreparable=0, dropped_vcs_unverified=0, deduped_total=28`. No repair round-trip was needed (normalisation handled all 37 drift cases deterministically). L5 and L7 returned prose instead of strict JSON; their content was mechanically converted during normalisation.

## Scope

- **Languages / runtimes**: Go 1.26.3 (all production code), Python 3 (three script processors), Bash + PowerShell (installers + hooks)
- **Direct Go dependencies**: `github.com/Microsoft/go-winio v0.6.2`, `golang.org/x/sys v0.20.0`, `gopkg.in/yaml.v3 v3.0.1`
- **External model**: Gemma 4 E4B GGUF (`unsloth/gemma-4-E4B-it-GGUF`, UD-Q4_K_XL, 5.1 GB), SHA-256 pinned at build time via `-ldflags -X`
- **External runtime**: `llamafile` (Mozilla), spawned as a private subprocess on a private localhost port
- **AI components**: **yes** — a locally-hosted Gemma 4 inference daemon, a router that calls the model to pick a processor chain, and prompt processors with markdown system prompts
- **Entry points**: `cmd/thlibo` (middleware CLI; invoked by the AI client's PreToolUse hook), `cmd/thlibod` (inference daemon; autostarted via launchd / systemd --user / Windows Startup folder)
- **Agentic risk factors present**: Non-Determinism (temperature 1.0 in compress/casefolder prompts; temperature 0.0 in router), Autonomy (middleware autonomously rewrites commands before they execute in Claude Code), Agent Identity (one human user = one daemon = one warm model; no identity federation), A2A (processor-chain `stdout → stdin` piping)

## Risk Summary

| # | ASI Threat | Layer | Title | Severity | L | I | Risk | Agentic Factors | Framework |
|---|---|---|---|---|---|---|---|---|---|
| 1 | T6, BV-4 | L1 | Tool-output bytes flow into model prompt without escaping of `<|tool_call|>` / `<|channel|>` markers | **high** | 3 | 3 | **9** | Non-Det, A2A | STRIDE:T / LLM01 / ASI02 |
| 2 | T13, BV-3 | L7 | GitHub Actions pinned by mutable tag, not commit SHA (release.yml, ci.yml, pages.yml) | **high** | 2 | 3 | **6** | — | OWASP A06 / ATT&CK T1195.002 |
| 3 | T6 | L1 | Router prompt embeds truncated tool output (first 200 chars) without escaping, bypasses routing logic | medium | 3 | 2 | 6 | Non-Det | LLM01 / ASI02 |
| 4 | T1 | L2/L6 | SHA-256 pin verified with non-constant-time `equalFold`; should be `subtle.ConstantTimeCompare` | medium | 2 | 3 | 6 | — | CWE-208 |
| 5 | T20, T12 | L2 | Daemon's NDJSON frame reader has no per-frame size cap (middleware has 64 MiB cap; daemon does not) | medium | 2 | 3 | 6 | — | CWE-400 |
| 6 | T4 | L4 | `systemd --user` unit uses `Restart=on-failure` with no `StartLimitIntervalSec` cap; daemon internal cap is 3 but systemd will loop | medium | 2 | 3 | 6 | — | CWE-400 |
| 7 | BV-2, T29 | L3/L7 | User processor in `~/.thlibo/processors/<name>/` silently shadows built-in of same name; no startup warning | medium | 2 | 3 | 6 | A2A | OWASP A05 |
| 8 | T22 | L2/L5 | Subprocess stderr is logged verbatim by `execcmd`; if a failing processor emits a secret to stderr it lands in `~/.thlibo/logs/*.ndjson` | medium | 2 | 3 | 6 | — | CWE-532 |
| 9 | BV-9 | L3 | Descriptor read at registry-load; entry file executed at dispatch-time with no re-stat | medium | 1 | 3 | 3 | — | CWE-367 |
| 10 | T44 | L5 | `thlibod` startup path uses stdlib `log.Printf`, not `logx`; daemon boot events never land in NDJSON audit trail | medium | 2 | 2 | 4 | — | CWE-778 |
| 11 | T44 | L5 | SHA-256 mismatches on `thlibo pull` return an error to the user but are not written to NDJSON audit log | medium | 2 | 2 | 4 | — | CWE-778 |
| 12 | T44 | L5 | Router's `parseRoutingResponse` silently drops unknown processor names; no audit entry for adversarial routing attempts | medium | 2 | 2 | 4 | Non-Det | CWE-778 |
| 13 | T23 | L5 | Single-generation log rotation (`.ndjson.old`); no tamper detection; second rotation erases prior audit window | medium | 2 | 2 | 4 | — | CWE-778 |
| 14 | T3 | L4/L6 | `thlibod` systemd user unit lacks `NoNewPrivileges=true`, `PrivateDevices=true`, `ProtectHome=read-only` etc. | low | 2 | 2 | 4 | — | CWE-250 |
| 15 | MA-2 | INTEGRITY | Claude Code hook emits `permissionDecision: allow` for every rewritten Bash command (intentional but undeclared in threat model before this review) | medium | 2 | 2 | 4 | Autonomy | ASI02 |
| 16 | MA-6 | INTEGRITY | Single install step writes a persistent PreToolUse hook into `~/.claude/settings.json`; interception survives across all future Claude Code sessions | medium | 2 | 2 | 4 | Autonomy | CWE-427 |
| 17 | T32 | L4 | No per-connection rate-limit on daemon; queue full returns immediately but a single caller can monopolise by re-submitting after each reject | low | 2 | 2 | 4 | — | CWE-400 |
| 18 | T20 | L1 | `engine.Generate` composes JSON via `fmt.Sprintf("%q", prompt)` instead of `json.Marshal` (works, but brittle) | low | 1 | 2 | 2 | — | CWE-20 |
| 19 | T7 | L1 | `thoughtBlockRE` uses non-greedy `(?s)(.*?)`; nested or malformed delimiters could cause mis-stripping and `<think>` leakage into Claude Code | low | 1 | 2 | 2 | Non-Det | LLM08 |
| 20 | T22 | L6 | `.gitignore` missing `*.jks`, `*secret*`, `id_rsa*`, `*.gpg`, `*.asc` (present: `.env*`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `credentials.json`, `secrets.yaml`) | low | 1 | 2 | 2 | — | CWE-532 |
| 21 | T3 | L4 | `/run/thlibo/thlibod.lock` created without verifying parent is not a symlink; TOCTOU on first install if `/run/thlibo` is pre-created hostile | low | 1 | 2 | 2 | — | CWE-367 |
| 22 | T11 | L4 | `cmd/thlibo/exec` has no allow-list of executables; relies entirely on the AI client for command safety (by design, but worth naming) | low | 1 | 2 | 2 | Autonomy | CWE-78 |
| 23 | T46 | L7 | HuggingFace TLS is the only network-policy enforcement on the model download; no host allow-list or redirect cap (`curl --max-redirs 0` not set in `release.yml`) | low | 1 | 2 | 2 | — | CWE-918 |
| 24 | T9 | L6 | Daemon performs no `SO_PEERCRED` check; IPC identity is purely the socket-permission bits (0660 group `thlibo-users`). Matches design, but not defence-in-depth | low | 1 | 2 | 2 | — | CWE-287 |
| 25 | BV-2 | L3 | Processor `description` field in YAML is user-controlled but is shown to the router model; attacker-authored description can bias routing | low | 1 | 2 | 2 | Non-Det | ASI05 |
| 26 | T22 | L6 | Installer docstring claims it "creates the thlibo-users group" but `internal/install/install.go` never calls `groupadd`; group must exist a priori for 0660-GID chown to succeed | low | 2 | 1 | 2 | — | CWE-532 |
| 27 | T14 | L7 | `install.sh` / `install.ps1` fetch SHA256SUMS from the *same* GitHub release as the binaries; a release-level compromise would substitute both | info | 1 | 2 | 2 | — | CWE-494 |
| 28 | T25 | L7 | No SBOM, no release-provenance attestation (cosign / SLSA), no signed artefacts | info | 1 | 2 | 2 | — | — |

Risk = likelihood × impact after agentic-factor modifiers (+1 likelihood for non-determinism, +1 impact for autonomy / A2A; both capped at 3). Bands: Low 1–2, Medium 3–4, High 6, Critical 9.

## Layer Analysis

### Layer 1 — Foundation Model

The local Gemma 4 E4B model is source-trusted (unsloth/HuggingFace, SHA-pinned at build time). The **T1 integrity story is sound in intent**: the pin is an immutable `-ldflags -X` string baked into the compiled binary, and `verifySHA` (`internal/install/model.go:311-325`) recomputes a full SHA-256 over the downloaded `.part` file before the atomic rename. The only nit is **finding #4**: the comparison uses a hand-rolled `equalFold` loop (`model.go:369-388`) that exits early on the first mismatched byte. For a 64-character hex digest, the timing leak is not exploitable remotely (the attacker would need to influence the download *and* observe the daemon's wall-clock response), but `crypto/subtle.ConstantTimeCompare` is the correct primitive and would silence future security-scanner noise.

The real L1 problem is **T6 Intent Breaking / BV-4 Prompt Leakage via Tool Outputs** (findings #1 and #3). Three converging data paths carry untrusted bytes into the model without escaping:

1. `internal/middleware/middleware.go:174-205` — `PromptRunner.Run` wraps the raw tool output as an `ipc.Message{Role:"user", Content: <raw bytes>}`. A `git diff` that happens to contain a Gemma native tool-call marker (e.g. a README code block demonstrating one) is passed through verbatim.
2. `internal/router/router.go:81-100` — `buildRoutingMessages` truncates the input to 200 chars and embeds it in the routing prompt as the user turn. If those 200 chars end mid-escape, the model can interpret the fragment as control.
3. `internal/daemon/lifecycle.go:412-425` — the daemon sorts messages by role and hands `.System` and `.User` to `engine.Generate` as-is.

Mitigation: the router *is* already constrained by a GBNF grammar (`router.go:124-140`) that only permits registered processor names, which blunts the worst-case "hallucinate an arbitrary chain" attack. But the grammar does not protect the **compress / casefolder** prompt-processor turns, which are free-form. A bounded escape of the two Gemma markers (`<|tool_call|>` and `<tool_call|>`) before wrapping tool bytes as a user message would close the gap with no quality impact — the target tokens simply don't occur in real `git`/`npm`/`cargo` output.

Minor L1 notes: `thoughtBlockRE` (`internal/processors/thinking.go:26`) is greedy-non-greedy; a malformed model output with nested `<|channel|>thought` tokens could survive stripping and leak reasoning text back to Claude Code (finding #19). Router temperature is pinned at 0.0 (`router.go:56`) for determinism — this is correct for classification and *not* a security bug despite L1-9's flag.

### Layer 2 — Data Operations

thlibo has no database and no RAG, so this layer is mostly about YAML, NDJSON, and the GGUF download path.

YAML parsing is handled correctly. `ParseYAML` (`internal/processors/descriptor.go:94-106`) and `ParseMarkdown` (lines 117-120) both call `dec.KnownFields(true)`, which rejects unknown fields at decode time — the go-to defence against gadget-chain injection through `yaml.v3`. The `entry:` field is further constrained to a plain filename (`descriptor.go:196-198`) — any `/` or `\` fails validation, which rules out path traversal through descriptor files. This is well-tested (`TestEntryMustBePlainFilename`, `descriptor_test.go:168-179`).

NDJSON is where the layer bleeds. **Finding #5**: `internal/ipc/protocol.go:169-178` reads inbound frames with `bufio.Reader.ReadBytes('\n')`. Go's `bufio.Reader` auto-grows its buffer until the line is complete. The middleware side has a 64 MiB `io.LimitReader` at `middleware.go:155-161`, but the daemon side (`lifecycle.go:366-370`) does not. A local client that opens the inference socket and writes a 1 GB blob without a newline will slowly drag the daemon's RSS up until the OS OOM-killer resolves the situation.

The GGUF download pipeline (`cmd/thlibo/pullcmd/pull.go` + `internal/install/model.go`) is a small chain worth naming:
- Range-resume is supported for interrupted downloads; the destination is always `filepath.Join(opts.Dir, m.Filename) + ".part"`, with `m.Filename` pinned in the binary (`model.go:67-72`).
- After the full download, `verifySHA` computes a fresh SHA-256 and compares against the `-ldflags`-pinned string.
- On mismatch, the `.part` is deleted and an error is returned to the user.
- **Gap**: the mismatch is *not* written to the NDJSON log (finding #11) — an attacker-triggered mismatch is visible only in the current user's terminal.

Logging of sensitive bytes: thlibo is careful to log byte counts (`raw_bytes`, `out_bytes`, `reduction_pct`) not content. But `trimStderr` (`internal/processors/dispatch.go:122-128`) captures the last 200 chars of a failed processor's stderr and logs it. If a processor's Python code crashes mid-API-call with `print(f"Error: TOKEN={os.environ['API_KEY']}", file=sys.stderr)`, the token lands in `~/.thlibo/logs/thlibo-exec.ndjson`. Finding #8 covers this; a regex-based pre-log redaction is the natural fix.

### Layer 3 — Agent Frameworks

The processor plug-in system is the most agent-like part of thlibo and it carries the expected class of findings.

**Rug-pull (BV-2, finding #7)**: `internal/processors/registry.go:125-129` gives user processors at `~/.thlibo/processors/<name>/` unconditional priority over built-ins. Drop a `compress/processor.yaml` pointing at `run.py` next to a `run.py` that calls `curl evil.example.com` and the next `thlibo exec` invocation routes compression through it. This is the documented trust model ("the user owns their plug-in dir"), but **no startup warning fires** when shadowing happens. A single `logx.Warn("processor %s shadows built-in", name)` at registry-build time would close the information-asymmetry gap without changing the security model.

**TOCTOU (BV-9, finding #9)**: `Registry.scan` reads descriptors at middleware startup; `Dispatcher.runScript` (`internal/processors/dispatch.go:56-93`) executes the entry file on each invocation. Between the two moments, an attacker with write access to `~/.thlibo/processors/<n>/run.py` can swap the file. The probability is low (requires the attacker to already own the user's plug-in dir, which implies game-over) but defence-in-depth would be a mtime/inode re-check at dispatch time.

**Shell injection**: not a finding. `Dispatcher.runScript` uses `exec.CommandContext(bin, args...)` (not `/bin/sh -c ...`), and `EntryCommand` restricts `bin` to `python3`, `bash`, or a direct exec of the entry file. `args` is always a single pre-validated basename. The `// nosemgrep` annotation at `dispatch.go:73` is correctly justified.

**Grammar-enforced routing**: `internal/router/router.go:124-140` — `buildGrammar()` emits GBNF that constrains Gemma to the exact `<|tool_call|>call:route{processors:[...]}<tool_call|>` form, with processor names enumerated from the registry. An attacker who gets a foothold in the prompt-processor turn cannot make the router produce an unknown name. Good. L1 finding #1 still applies to the prompt-processor turn itself; the grammar doesn't reach the content prompts.

### Layer 4 — Deployment Infrastructure

Per-user, no-admin, no-shim installation is a deliberate attack-surface reduction (ADR 0003, ADR 0004). Most of the L4 surface is well-bounded.

**Resource-overload gap (finding #6)**: `internal/install/autostart_linux.go:79-102` generates a systemd user unit with `Restart=on-failure` and `RestartSec=2` but **without** `StartLimitIntervalSec=60 StartLimitBurst=3`. The daemon's internal `MaxRestartAttempts=3` stops the *llamafile* subprocess from looping, but if the *daemon itself* dies, systemd will happily restart it every 2s forever. The fix is a two-line unit-file change.

**CI supply chain (finding #2)**: Every `.github/workflows/*.yml` file uses tag-based action references — `actions/checkout@v4`, `actions/setup-go@v5`, `softprops/action-gh-release@v2`, `gitleaks/gitleaks-action@v2`. Tags are mutable. The minor release of an action can be force-pushed by its author (or a compromised account) to point at a different commit. A malicious `setup-go@v5` during `release.yml` would have access to `GITHUB_TOKEN` with `contents: write` and could substitute binaries in the release before `softprops/action-gh-release` uploads them. Pinning each `uses:` to a commit SHA and using Renovate/Dependabot for controlled updates is the industry-standard fix.

**Daemon hardening (finding #14)**: the systemd user unit lacks `NoNewPrivileges=true`, `PrivateDevices=true`, `ProtectHome=read-only` (minus `~/.thlibo`), `ProtectSystem=strict`, `ReadWritePaths=%h/.thlibo`. None of these are *required* for correctness (the daemon already runs as the user), but each raises the bar if a future CVE in llamafile allows sandbox escape.

**No TLS pinning for the HuggingFace pull**: `release.yml:32-34` and `internal/install/model.go:193-210` both trust the default Go / curl redirect policy. A HuggingFace CDN compromise, DNS poisoning, or a mis-issued HuggingFace certificate are theoretical; the SHA-256 pin catches the attack *after* the download. Accepting the default is fine for v0.1.0 but worth naming (finding #23).

**Windows named pipe**: `internal/ipc/endpoint_windows.go` constructs an SDDL with `D:PAI(A;;GRGW;;;<user-SID>)` (user-only read/write). `currentUserSID()` is the only source of the SID. I did not find a path where an empty SID could silently widen the pipe's DACL — `winio.ListenPipe` rejects malformed SDDL with an explicit error — but the finding is worth keeping as a low-confidence note (part of finding #24).

### Layer 5 — Evaluation & Observability

thlibo's logging is intentionally small: a single `logx.Logger` writing NDJSON to `~/.thlibo/logs/*.ndjson` with size-based rotation to `.ndjson.old`. There is no alerting, no HITL, no external log sink. For a local user-space tool, this is a reasonable minimum.

**T44 cluster (findings #10, #11, #12)** — three classes of security-relevant events that are either logged to stdlib `log.Printf` (which doesn't land in NDJSON) or silently dropped:
1. `cmd/thlibod/main.go` boot errors use stdlib `log.Printf` — never reach the audit trail.
2. `thlibo pull` SHA mismatches return a user-facing error but never call `logger.Error`.
3. `internal/router/router.go:160-185` (`parseRoutingResponse`) drops unknown processor names returned by Gemma with no log entry — the exact signal an operator would want when investigating an adversarial tool-output that confused the router.

**T23 log tamper (finding #13)**: single-generation rotation (`logx.go:230-235`) is aggressive. A second rotation wipes the previous `.old`. For a forensics-relevant window, an attacker under the same UID can force rotation (write many debug records with `THLIBO_LOG=debug`), then allow the second rotation to erase the evidence. Keeping a rolling `.old`, `.old.1`, `.old.2` (or streaming to syslog/journald on Linux) would address this. File permissions are 0o600 on the files and 0o750 on the directory; moving the directory to 0o700 is the minor hardening from finding #15.

### Layer 6 — Security & Compliance

The identity model is **"the OS user who owns the process"** — there is no server-side auth because there is no remote caller. The trust boundaries are enforced at the filesystem / socket / pipe level:

| Boundary | Unix | Windows |
|---|---|---|
| `thlibo` CLI → `thlibod` daemon | `/run/thlibo/infer.sock` mode 0660 group `thlibo-users` | `\\.\pipe\thlibo-infer` SDDL restricted to user SID |
| `thlibod` daemon → `llamafile` | stdio of private child + private localhost port | same |
| Installer → HuggingFace | TLS 1.2+ default Go client + build-time SHA-256 pin | same |

**Finding #4 / #7 (SHA compare)**: as noted in L1 / L2, `equalFold` is not constant-time. One-line fix.

**VCS hygiene** (L6 duty, informational / low):

| Check | Result |
|---|---|
| `.gitignore` exists at repo root | ✅ |
| Secrets patterns (`.env*`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `credentials.json`, `secrets.yaml`) | ✅ |
| Missing (recommended) patterns (`*.jks`, `*secret*`, `id_rsa*`, `*.gpg`, `*.asc`) | finding #20 |
| Build artefacts (`/bin/`, `/dist/`, `*.exe`, `*.test`, `coverage.*`) | ✅ |
| Model artefacts (`*.gguf`, `*.safetensors`, `/models/`, `~/.thlibo/models/`) | ✅ |
| IDE / OS (`.vscode/`, `.idea/`, `*.swp`, `.DS_Store`, `Thumbs.db`) | ✅ |
| Any tracked file matches an ignore pattern | none found (cross-checked `git ls-files` ∩ ignore patterns) |

**Docstring vs code drift (finding #26)**: `internal/install/install.go:1-6` states that `thlibo install` "creates the `thlibo-users` group". The actual implementation never calls `groupadd` — the Unix IPC chown needs the group to exist a priori, or falls back silently to the daemon's primary group (which, given per-user deployment, is just the user's login group). The practical effect is a cosmetic claim drift, not a privilege hole.

### Layer 7 — Agent Ecosystem

Dependencies (from `go.sum`): three direct, one transitive test-only (`gopkg.in/check.v1`). `govulncheck v1.1.4` returned **zero vulnerabilities** against the 2026-05-07 Go vuln DB. This is the simplest SBOM story thlibo will ever have; that's partly why the release-signing / SBOM gap (finding #28) feels larger than the actual current risk.

**Supply-chain surface**:
- **Installers (`install.sh`, `install.ps1`)**: verify downloaded archives against SHA256SUMS. SHA256SUMS is fetched from the same release (finding #27). If a release is compromised, both the archive and the checksum are compromised; the user's only integrity signal would be a mismatch against a known-good manually-recorded SHA, which nobody does. Mitigation would be cosign-signed release attestations (SLSA level 2+), published separately. ADR-worthy.
- **Build pipeline (`release.yml`)**: tag-pinned actions (finding #2), `contents: write` permission (scoped correctly), GGUF download without host allow-list (finding #23).
- **Processor ecosystem**: a rogue processor in `~/.thlibo/processors/` runs with full user privileges (finding #7). Built-ins are embedded via `go:embed` into the thlibo binary and are source-reviewed; this is the strong side of the ecosystem.

**Hook-envelope trust (from INTEGRITY review)**: `internal/adapters/claudecode/hook.sh:60-81` emits `"permissionDecision": "allow"` for every successfully rewritten Bash command. This is the documented design — the whole point of the hook is to transparently rewrite commands. But it *does* mean thlibo effectively bypasses Claude Code's per-command permission prompt for any Bash invocation that matches the PreToolUse filter. Finding #15. The "ask" path at `hook.sh:61-69` (triggered by `thlibo exec`'s exit code 3) is the documented escape hatch for the user when thlibo chooses to defer. The finding is not that this is a bug — it's that **the threat model must acknowledge the delegation**: once the hook is installed, every Bash tool-call in every future Claude Code session runs through thlibo's (now-trusted) decision logic. Finding #16 is the persistence-injection counterpart.

## Agent / Skill Integrity

| File | Type | Declared Intent | Misalignment | ASI Threat | Evidence | Severity | Observable |
|---|---|---|---|---|---|---|---|
| `internal/adapters/claudecode/hook.sh:76` | shell hook | "PreToolUse compressor" | Emits `permissionDecision: allow` unconditionally on the rewrite path | MA-2 / T2 | Hook bypasses Claude Code's per-command permission prompt for all Bash invocations the matcher catches | medium | yes (visible in rewritten command; logged by thlibo exec) |
| `internal/router/router.go:99` | in-process agent | "Compress tool output safely" | Ingests untrusted stdout into a model prompt with no token-level sanitisation | MA-7 / T6 | `buildRoutingMessages` puts `truncate(input, 200)` into a `RoleUser` message | medium | no (router decisions are logged; the *input* to the decision is not) |
| `README.md` + `cmd/thlibo/pullcmd/pull.go:106-141` | CLI | "No network calls, no telemetry, nothing leaves localhost" | `thlibo pull` fetches the GGUF from HuggingFace; not all users think of that as "thlibo makes a network call" | MA-4 / T19 | README lines 76-77, 354-369 claim local-first; `pull` subcommand contradicts at first install | medium | yes (network socket + 5 GB download are hard to miss, but README doesn't name it) |
| `internal/install/install.go` (writes `~/.claude/settings.json`) | installer | "One-time installer" | Writes a persistent PreToolUse hook that intercepts every future Bash invocation in Claude Code | MA-6 / T13 | `MergeSettings` mutates `~/.claude/settings.json`; idempotent re-install but permanent until explicit uninstall | medium | yes (user can `cat ~/.claude/settings.json`; no UI notification) |
| `internal/install/mirror.go:48-72` | installer | "Mirror embedded processors to disk with restrictive perms" | Script entry files written mode 0700; directory mode 0750 allows group-readable listing | MA-2 / T45 | Dir `~/.thlibo/processors/` is 0750; script files are 0700 owner-exec | low | yes (visible in `ls -l`) |
| `processors/compress/processor.md` | prompt processor | "Compress progress spinners, repeated messages, blank runs" | Wording is permissive ("drop blank-line runs"); a weak-aligned model could drop error lines that *look* like noise | MA-3 / T7 | Frontmatter `temperature=1.0` + greedy prompt language | low | partial (fallback-on-error catches crashes but not semantic drift) |

The shell-hook behaviour (row 1) is the biggest integrity item. It's not a bug — it's the design. But it needs to be named in public-facing threat docs so users don't assume that installing thlibo preserves Claude Code's default per-command permission UX.

## Dependency CVEs

`govulncheck ./...` against the 2026-05-07 vuln DB:

| Package | Version | CVE | CVSS | Fixed in | Reachable | Risk |
|---|---|---|---|---|---|---|
| — | — | **no vulnerabilities reported** | — | — | — | — |

Scanned with `govulncheck@v1.1.4` (DB: `vuln.go.dev`). Third-party direct deps: `github.com/Microsoft/go-winio v0.6.2`, `golang.org/x/sys v0.20.0`, `gopkg.in/yaml.v3 v3.0.1`. Stdlib Go 1.26.3. Clean run.

`trivy fs .` was not run because the repo has no containers and govulncheck already covers Go. Python script processors (`processors/*/run.py`) use only stdlib — no `requirements.txt`, nothing for `pip-audit` to scan.

**Trivy supply-chain advisory (CVE-2026-33634)**: noted, not applicable — trivy was not invoked for this scan. Per policy, the blocklist (trivy 0.69.4/5/6 containers and hijacked Actions tags) would have been checked first.

## Status after remediation passes

Every finding in this document has a terminal state. The counts at
the top reflect what was surfaced in the original scan; **nothing
below is open**.

### Mitigated (code fix landed)

- **First remediation pass** (commit `d2b813e`): #1, #2, #4, #5, #6,
  #7, #8, #10, #11, #12, #14, #15, #20.
- **Second remediation pass** (commit `bc897a0`): #9, #13, #18, #19,
  #21, #26.
- **v0.2 feature work** (commit `dd74c3a`): #27, #28.
- **v0.2 hardening pass** (subsequent commit): #16, #17, #22, #24 —
  previously marked Accepted, now implemented as defence-in-depth
  because the forthcoming `inferd` split puts the same code in
  multi-tenant-capable territory where the "one local user" premise
  no longer holds. Details:
  - **#16**: new `thlibo uninstall` subcommand (reverses the
    installer cleanly, with `--purge` for full `~/.thlibo` wipe)
    and `$THLIBO_DISABLED=1` env gate honoured by every hook
    script.
  - **#17**: queue gains a per-caller concurrent-queued cap (4
    default) on top of the global 10-slot limit; identity comes
    from the new peer-cred path below.
  - **#22**: `~/.thlibo/policy.yaml` allow/deny list evaluated in
    `thlibo exec` before spawn. Default fall-through is allow;
    setting `default: deny` flips to block-unless-listed.
  - **#24**: `ipc.PeerIdentity` reads `SO_PEERCRED` on Linux and
    `GetNamedPipeClientProcessId` + `OpenProcessToken` on Windows.
    Daemon rejects UID/SID mismatches at accept time. Darwin falls
    through to the socket ACL until `LOCAL_PEERCRED` is wrapped in
    v0.3. TCP loopback is allowed-by-operator-choice (API-key
    challenge is a v0.3 item).

Each mitigation references its finding number in the commit message
and in the relevant package comment. `CHANGELOG.md` under
`[Unreleased]` has the per-finding description.

### Previously Accepted, now Mitigated

The four items below were initially marked Accepted on the grounds
that the v0.1 deployment model was strictly single-user. With
`inferd` planned as a host-wide daemon consumed by multiple
middlewares, those assumptions no longer hold, so the hardening
landed early in thlibo. See the "Mitigated" section above for the
commit reference.

- **#16 MA-6 persistence injection.** Persistence is still the
  product; the mitigation is the round-trip: `thlibo uninstall`
  exits cleanly, and `$THLIBO_DISABLED=1` is a per-session kill
  switch for users who don't want to remove anything.
- **#17 T32 per-caller rate limit.** Implemented as a per-
  `CallerID` concurrent-queued cap (default 4) on top of the
  global 10-slot limit. A noisy caller can no longer starve
  others.
- **#22 T11 exec allow-list.** Implemented as an opt-in
  `~/.thlibo/policy.yaml` evaluated before spawn. Default
  fall-through stays permissive; operators who want stricter
  posture set `default: deny`.
- **#24 T9 no SO_PEERCRED.** Implemented as defence-in-depth at
  IPC accept on both Unix (SO_PEERCRED) and Windows
  (GetNamedPipeClientProcessId + OpenProcessToken SID compare).

## Recommended Mitigations (Priority Order)

1. **(High, #1)** Escape Gemma native tool-call markers in tool-output bytes before they enter any model prompt. One location: add a `sanitizeForPrompt(s)` helper in `internal/middleware` that replaces `<|tool_call|>`, `<tool_call|>`, `<|channel|>` with visually-identical non-control runes (or HTML entities). Apply at `PromptRunner.Run` and at `router.buildRoutingMessages`. Unit test: property-test asserting no input containing those substrings survives to the daemon.
2. **(High, #2)** Pin every GitHub Actions `uses:` in `release.yml`, `ci.yml`, `pages.yml` to a commit SHA. Add a Dependabot config for `github-actions` ecosystem to keep them current without reintroducing tag floating. Takes ~10 minutes.
3. **(Medium, #4)** Swap `equalFold(got, want)` → `subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1` in `internal/install/model.go`. Three-line change.
4. **(Medium, #5)** Wrap the daemon-side reader with `io.LimitReader(conn, 64<<20)` (or `100<<20`) before `bufio.NewReader` in `lifecycle.go:366`. Add a negative test (`TestRejectsOversizedFrame`).
5. **(Medium, #6)** Add `StartLimitIntervalSec=60` and `StartLimitBurst=3` to the systemd user unit emitted by `autostart_linux.go`. Equivalent change for launchd: the `Throttle` / `ExitTimeOut` keys on macOS; Windows Startup has no equivalent (per-user executable, not a service).
6. **(Medium, #7)** Emit `logger.Warn("processor shadows built-in", "name", d.Name)` in `registry.go:125-129` when a user descriptor overrides a built-in. No security-model change; closes the information-asymmetry gap.
7. **(Medium, #8)** In `internal/logx/logx.go`, add a pre-write redactor that masks anything matching `AWS_[A-Z_]*=`, `ghp_[A-Za-z0-9]{36}`, `hf_[A-Za-z0-9]{30,}`, and a small other-token set before writing to disk. Apply only to string fields whose name is `stderr`, `reason`, or `fallbacks`.
8. **(Medium, #10-13)** Log the five currently-silent events: daemon boot/crash, SHA mismatch on pull, unknown router-response processor name, queue rejection reason (truncated to 1 KiB), engine restart. One `logger.Warn` / `logger.Error` call each.
9. **(Medium, #15, MA-2)** Document in `README.md` and `CHANGELOG.md` the security implication of the PreToolUse auto-allow path: *after install, every Bash tool call in Claude Code that matches the hook matcher runs through thlibo's rewrite without re-prompting the user*. Mitigation for the worried user: set `permissionDecision` to `"ask"` in the rewrite path. Optional `THLIBO_CONFIRM=1` env gate would be a useful v0.2 feature.
10. **(Low, #20)** Add `*.jks`, `*secret*`, `id_rsa*`, `*.gpg`, `*.asc` to `.gitignore`.
11. **(Info, #27, #28)** Plan for v0.2: cosign-signed release attestations (SLSA 2 target) published to the Sigstore transparency log; SBOM generation in `release.yml` via `cyclonedx-gomod`. Blocks supply-chain scenarios that the SHA256SUMS-on-same-release model leaves open.

## Trust Boundaries

```
   ┌────────────────────┐
   │   Human developer  │
   └─────────┬──────────┘
             │  TB-0  (workstation)
   ┌─────────▼──────────┐
   │  Claude Code /     │
   │  Codex CLI         │
   │  (AI client proc)  │
   └─────────┬──────────┘
             │  TB-1  (hook envelope: JSON on stdin,
             │         MUTUAL user-UID trust)
   ┌─────────▼──────────┐
   │  thlibo CLI        │  <─── reads descriptors from ~/.thlibo/processors  (TB-P)
   │  (middleware)      │       (T2 / BV-2 lives here: user-installed code)
   └─────────┬──────────┘
             │  TB-2  (Unix socket 0660 group thlibo-users │
             │         OR Windows named pipe SDDL=user-SID │
             │         OR loopback TCP 127.0.0.1:47320)
   ┌─────────▼──────────┐
   │  thlibod daemon    │
   └─────────┬──────────┘
             │  TB-3  (subprocess stdio + private localhost port)
   ┌─────────▼──────────┐
   │  llamafile         │  (serves Gemma 4 E4B)
   └─────────┬──────────┘
             │  TB-4  (read-only mmap of ~/.thlibo/models/*.gguf;
             │         integrity anchored by build-time SHA pin)
             ▼
        GGUF on disk

   ┌────────────────────┐
   │ HuggingFace CDN    │  <── TB-5  (TLS 1.2+, SHA-pinned download)
   └────────────────────┘          (touched once, at `thlibo pull`)
```

Every boundary is a one-way dependency: no inbound connections cross any `TB-n` from the right — thlibo has no listener other than the Unix socket / pipe that its own middleware talks to.

## Data Flow Diagram

```
Claude Code                  thlibo CLI                   thlibod                    llamafile
(Bash tool)                  (middleware)                 (daemon)                   (Gemma 4)
    │                             │                          │                            │
    │ PreToolUse hook.sh          │                          │                            │
    │ { cmd, stdout_bytes }       │                          │                            │
    ├────────────────────────────▶│                          │                            │
    │                             │ length < 2000 ──► passthrough, early return            │
    │                             │                          │                            │
    │                             │ scan ~/.thlibo/processors│                            │
    │                             │ + embedded built-ins     │                            │
    │                             │                          │                            │
    │                             │ fast-path regex match?   │                            │
    │                             │ ├─ yes ──► script proc   │                            │
    │                             │ │  (stdin/stdout pipe)   │                            │
    │                             │ │                        │                            │
    │                             │ └─ no ──► router prompt  │                            │
    │                             │          {msgs, temp=0}──┼────────────────────────────▶
    │                             │                          │ grammar-constrained gen    │
    │                             │          ◀───────────────┼──── {processors:[...]}     │
    │                             │                          │                            │
    │                             │ run chain, stdout→stdin  │                            │
    │                             │ any processor error      │                            │
    │                             │ → fall back to original  │                            │
    │                             │                          │                            │
    │ { updatedInput.command,     │                          │                            │
    │   permissionDecision:allow }│                          │                            │
    │◀────────────────────────────│                          │                            │
    │                             │                          │                            │
   exec the (possibly rewritten)  │                          │                            │
   command, return result         │                          │                            │
```

Every lateral arrow ends in a known artefact: the hook envelope JSON, the IPC NDJSON, or the GBNF-constrained model output. The only unbounded free-form data that touches the model is the **content** of the prompt-processor turn — which is exactly what L1 finding #1 names.

---

## Addendum — 2026-07-14: opt-in OpenTelemetry egress (ADR 0011)

The body of this document is a point-in-time MAESTRO snapshot of
v0.1.0. This addendum records a deliberate posture change introduced
after that snapshot; it does not rewrite the immutable risk table
above.

**What changed.** ADR 0011 adds optional OpenTelemetry emission
(metrics + events) to a collector the operator configures. This is a
new network-egress path and it **supersedes, for the telemetry-enabled
configuration, the MA-4/T19 integrity claim** in the Agent/Skill
Integrity table above ("No network calls, no telemetry, nothing leaves
localhost"). With telemetry enabled, thlibo *does* make an outbound
OTLP call. That claim remains accurate in thlibo's **default**
configuration: telemetry is off unless `THLIBO_ENABLE_TELEMETRY` is
set, and when off no SDK is constructed and no socket is opened.

**Threat decisions.**

| Threat | Decision |
|---|---|
| **T19 / MA-4 — undisclosed egress.** A tool that claimed "nothing leaves localhost" now can emit. | **Mitigated by disclosure + opt-in.** Off by default; the README "Monitoring" section and ADR 0011 name the egress explicitly; the enable flag is an affirmative operator action. |
| **BV-4 / LLM06 — data exfiltration via telemetry content.** Tool output, prompts, commands, or file paths could leak through metric/event attributes. | **Mitigated by construction.** thlibo emits only sizes, counts, durations, and a fixed enum-label set. There is no content-capture opt-in of the kind Claude Code offers (`OTEL_LOG_USER_PROMPTS` et al.). The sole variable-cardinality label — processor name — emits verbatim only for the closed, source-reviewed **built-in** set; **user** processor names redact to the constant `"custom"`. A privacy regression test asserts no raw input bytes reach any exported attribute. |
| **T32 / CWE-400 — a slow/hostile collector degrades the client.** thlibo sits in the critical path of every matched tool call. | **Mitigated by fail-open + bounded flush (ADR 0006, 0011).** Every telemetry op is best-effort; export failure or timeout drops data silently and never changes the tool output bytes or exit code. The on-exit flush is capped at a **fixed 2 s** (not operator-configurable). Against the recommended localhost collector the flush is microseconds; a misconfigured/unreachable **remote** endpoint makes each emitting invocation wait up to that 2 s ceiling before dropping the batch — the deliberate cap that stops a dead collector from hanging the client. A dead-endpoint test asserts the flush returns within the bound and the tool output is byte-identical. |
| **T13 / CWE-522 — credentials in telemetry config.** `OTEL_EXPORTER_OTLP_HEADERS` may carry a bearer token. | **Accepted, operator-owned.** Same handling as the AI client's own OTLP auth; thlibo reads it from the process environment, never logs it, and never writes it to the NDJSON trail. |

**Trust-boundary delta.** A new one-way outbound boundary is added,
active only when telemetry is enabled:

```
   ┌─────────▼──────────┐
   │  thlibo CLI        │
   │  (middleware)      │
   └─────────┬──────────┘
             │  TB-6  (OTLP → operator collector, opt-in via
             │         THLIBO_ENABLE_TELEMETRY; content-free;
             │         fail-open, bounded flush; recommended
             │         localhost:4318)
   ┌─────────▼──────────┐
   │ Operator's OTel    │
   │ collector          │   (storage/dashboards are the operator's)
   └────────────────────┘
```

Like every other boundary, TB-6 is one-way: thlibo pushes; nothing
inbound crosses it. The collector, its storage, and any onward
forwarding are the operator's responsibility, outside thlibo's trust
boundary.
