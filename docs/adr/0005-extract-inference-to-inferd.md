# 0005. Extract inference to a separate `inferd` service

- Status: accepted
- Date: 2026-05-18

## Context

v0.1–v0.5 ship two binaries: `thlibo` (middleware) and `thlibod`
(inference daemon). The daemon embeds llamafile, owns model
download + verification, runs the request queue, and is wired into
thlibo's installer via per-platform autostart units.

Operating in this shape has produced three frictions:

1. **Coupled release cadences.** Every change to inference
   internals — model swaps, llamafile upgrades, queue tuning,
   admin-socket protocol revisions — forces a thlibo binary
   rebuild even when the middleware itself is unchanged.
2. **Single-consumer model store.** The daemon owns the GGUF on
   disk under `~/.thlibo/models/`. Other local-AI tooling on the
   same machine re-downloads identical bytes into its own
   directory. See [`.plan/spec.issue.md`](../../.plan/spec.issue.md)
   for the proposed shared-store convention this opens up.
3. **Surface area mismatch.** Thlibo's job is rewriting tool
   calls and dispatching to processors. Inference engine
   supervision, model fetch, NDJSON wire encoding, queue admission
   — none of those have anything to do with rewriting Bash tool
   calls. Carrying them in the same repo dilutes both projects.

Three shapes were considered:

1. **Status quo.** Keep `thlibod` embedded in thlibo. Simplest
   ops story; one repo, one release. Locks in all three
   frictions above.
2. **In-process library.** Import the inference engine as a Go
   package; thlibo owns its lifecycle in-process. Reduces a
   binary but couples crash domains — a llamafile FFI panic
   takes down the middleware and every Claude Code Bash hook
   along with it.
3. **Out-of-process sidecar.** Inference moves to its own
   project (`inferd`, written in Rust against vendored
   llama.cpp), distributed independently, consumed by thlibo
   over the existing IPC. Thlibo becomes a pure middleware.

## Decision

Adopt option 3. Extract inference into `github.com/3rg0n/inferd`,
distributed as a sidecar binary plus language-native client
crates / modules. The clean line of separation:

| Moves to inferd | Stays in thlibo |
|-----------------|-----------------|
| `internal/daemon/` | Hooks (Bash / PowerShell / Read / Write / Edit) |
| `cmd/thlibod/` | `internal/middleware/` (short-circuit + fast-path + chain) |
| `internal/queue/` | `internal/processors/` (registry + script dispatch) |
| `internal/ipc/` (NDJSON wire types) | `internal/router/` |
| llamafile spawner / engine supervisor | `internal/adapters/` (claudecode, codex) |
| Model download + SHA verification (`internal/install/model.go`) | `internal/casefile/`, `internal/logx/`, `internal/promptsan/` |
| Restart cap, ready-gating, peer-cred check | Built-in script processors (git/npm/cargo/pytest/ndjson/stacktrace filters) |

Thlibo imports inferd's client module: a single flat Go package
at `github.com/3rg0n/inferd/clients/go`, version-pinned via
`go.mod`. The wire protocol is frozen as inferd protocol-v1 (see
[`docs/inferd-admin-protocol-v1.md`](../inferd-admin-protocol-v1.md));
backwards-additive changes within v1 are permitted, breaking
changes go to v2 on a separate socket path.

The inferd binary is not pulled in as a Cargo / Go package. It is
distributed as a signed release artefact from
`github.com/3rg0n/inferd/releases` — same pattern Postgres,
Redis, NATS, and etcd use for their server binaries.
`thlibo install` fetches the inferd binary at install time
(SHA-verified, cosign-validated), registers its autostart entry,
and continues with thlibo's own install steps.

The model store ownership transfers entirely. After this
extraction, the GGUF lives at `~/.inferd/models/` (pinned to the
same SHA — operators with the file on disk can symlink or copy
to skip a 5 GB redownload). Thlibo no longer knows where the
model is, what its hash is, or how it gets there.

## Consequences

**Easier:**

- Inference internals iterate independently. A llamafile bump or
  queue depth change ships from inferd without touching thlibo's
  release pipeline.
- The shared model store convention has a natural reference
  implementation: inferd's on-disk layout becomes the documented
  shape other tools can interop with.
- Thlibo's responsibilities narrow to "rewrite the Bash command
  and dispatch the result through a processor." Roughly half the
  current codebase deletes.
- Multiple consumers of inferd on the same machine share the
  warm model and the model bytes. A second tool integrating
  with inferd doesn't pay the model-load tax.
- Crash domains separate. An inferd panic doesn't take down
  Claude Code's Bash hook.

**Harder:**

- Two-binary install. `thlibo install` now also fetches and
  registers inferd. Mitigated by inferd providing its own
  installer that thlibo invokes — see ADR 0006 for the
  bootstrap-window contract.
- Dependency on a separate project's release cadence. If inferd
  ships a breaking change (within v2, never within v1), thlibo
  has to track it.
- Migration friction for v0.5 users. The model file moves
  paths; existing installs need a one-time symlink (or
  redownload). See `CHANGELOG.md` v0.6 entry for the operator
  procedure.
- Two repos, two CI matrices, two release pipelines, two threat
  models, two SBOMs. Operationally heavier even if the code is
  cleaner. Accepted as the cost of doing the split correctly.

## References

- inferd repo: `github.com/3rg0n/inferd`
- inferd protocol v1 (vendored): [`docs/inferd-admin-protocol-v1.md`](../inferd-admin-protocol-v1.md)
- ADR 0002 (single-daemon, single warm model) supersedes-link:
  superseded by inferd's own ADR 0002 once thlibo's daemon is
  removed.
- ADR 0006 (this repo): fail-open during inferd bootstrap window.
- Shared model store convention: [`.plan/spec.issue.md`](../../.plan/spec.issue.md)
