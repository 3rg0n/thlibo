# inferd protocol v1 — vendored copy

> **Stability:** Frozen per inferd ADR 0008. Backwards-additive
> changes within v1 (new optional fields older clients MUST
> ignore) are permitted; breaking changes go to v2 on a separate
> socket path.
>
> **Source of truth:** [`docs/protocol-v1.md` in the inferd repo](https://github.com/3rg0n/inferd/blob/main/docs/protocol-v1.md).
> This file is a vendored copy so thlibo's client code can be
> reviewed against the contract without context-switching into
> the inferd repo. If the two ever disagree, **inferd's copy
> wins** — open a PR to update this file.
>
> **Vendored from:** inferd v0.1.0 (planned), captured 2026-05-18.

---

## 1. Why this exists

The inference socket (`/run/inferd/infer.sock`,
`\\.\pipe\inferd-infer`, or loopback TCP) does not exist on disk
until the daemon is fully ready. That gives a passive readiness
signal: connect-with-retry succeeds when inferd is ready to
serve. Documented separately under §"Client connection
lifecycle."

The admin socket gives an active readiness signal plus rich
lifecycle events:

- First-boot download progress (5 GB GGUF, hours on slow links).
- Model load phases (download → verify → mmap → ready).
- Restart / drain events when an operator triggers them.
- Anything else the daemon needs to broadcast that isn't tied to
  a specific inference request.

Clients don't need it to use inferd. They do need it for progress
UX during first-boot bootstrap.

## 2. Endpoint paths

| Platform | Path | Permissions |
|----------|------|-------------|
| Linux    | `/run/inferd/admin.sock`      | mode 0600, owned by daemon uid |
| macOS    | `${TMPDIR}/inferd/admin.sock` | mode 0600, owned by daemon uid |
| Windows  | `\\.\pipe\inferd-admin`       | DACL grants current user SID only |

No TCP admin endpoint. Admin is local-only. A user-targeted
attacker on the same host needs the daemon's uid to read it;
cross-user attacks require POSIX permissions failure, not a flaw
in the protocol.

The path is configurable via `--admin-addr` / `INFERD_ADMIN_ADDR`
for testing and non-default deployments. Production deployments
use the default.

## 3. Lifecycle vs. inference: when does each socket exist

```
t=0   daemon process starts
t=0+  admin socket bound, accepting connections     ← clients can connect now
t=N   model present + loaded, backend reports ready
t=N+  inference socket bound, accepting connections ← inference clients can connect now
```

The admin socket is bound first, before any model work. Clients
connect, subscribe, and watch lifecycle events while the daemon
is bootstrapping. The inference socket comes up last — clients
that connect to inference do not need to consult the admin
socket; the connect succeeds only when inferd is fully ready.

## 4. Wire format

NDJSON, identical framing to the inference socket:

- One JSON object per frame, terminated by `\n`.
- UTF-8.
- 64 MiB cap per frame (THREAT_MODEL F-1).
- No pretty-printing.

Same `inferd-proto` Rust types serialise both directions if
building a Rust client.

### 4.1 Direction: server → client (push events)

The admin socket is read-only from the client's perspective in
v1. The daemon publishes events; the client consumes.
Client-to-server commands (e.g., drain, reload) are reserved for
v2.

A client that writes to the admin socket gets its bytes ignored.
The daemon may close the connection if it sees writes; clients
should not write.

### 4.2 Frame shape

Every admin event is a status frame per `docs/protocol-v1.md`.
Field set:

```json
{
  "id":     "admin",
  "type":   "status",
  "status": "<state>",
  "phase":  "<phase>",
  "detail": { }
}
```

- `id` is the literal string `"admin"`. Every admin frame uses
  this id; lets clients reuse the same frame parser as the
  inference socket without special-casing.
- `type` is the literal string `"status"`.
- `status` is one of the lifecycle states in §5.
- `phase` and `detail` are present on certain states (notably
  `loading_model`); see §5.

### 4.3 Connect behaviour

When a client connects to the admin socket:

1. The daemon immediately writes a snapshot frame carrying its
   current state. A client that connects mid-download gets a
   `loading_model` frame with progress, not a stale `ready`
   from before.
2. Subsequent state transitions are pushed as they happen.
3. The connection stays open indefinitely. The daemon does not
   close it; the client closes when done.

This means a client that connects, reads one frame, and
disconnects gets a usable point-in-time read of daemon state. A
client that stays connected gets the full event stream.

## 5. Lifecycle states

Five states. Always one and only one is current.

| `status` | Meaning |
|----------|---------|
| `starting` | Daemon process is up; admin socket is bound. No backend work yet. Brief; usually <100 ms. |
| `loading_model` | Model is being prepared. Carries `phase` + `detail`. May take seconds (mmap of cached file) or hours (5 GB first-boot download on a slow link). |
| `ready` | Inference socket is bound and accepting connections. The daemon is fully usable. |
| `restarting` | A previously-ready daemon is reloading the model (e.g. operator triggered `inferd-fetch` and SIGHUP'd the daemon). Inference socket is closed; new connections refused. Carries `phase` + `detail`. |
| `draining` | Daemon received a shutdown signal. Existing requests finish; new requests rejected. The daemon will exit shortly after this frame. |

State machine:

```
starting → loading_model → ready
                ↓             ↓
              (error)    restarting → loading_model → ready
                ↓             ↓             ↓
            draining       draining       draining → exit
```

### 5.1 `loading_model` phases

When `status: "loading_model"`, the frame includes a `phase`
field naming the sub-stage:

| `phase` | Meaning | `detail` fields |
|---------|---------|-----------------|
| `checking_local` | Resolving model path on disk and checking SHA-256. | `path` (string) |
| `download` | Downloading the GGUF. Progress events emitted periodically (every 32 MiB or every 5 seconds, whichever first). | `downloaded_bytes` (int), `total_bytes` (int, may be null if server didn't send Content-Length), `source_url` (string) |
| `verify` | Streaming SHA-256 over the downloaded bytes for final verification. | `path` (string) |
| `quarantine` | Downloaded SHA mismatched config; file moved to `<name>.quarantine.<rfc3339>`. Daemon is about to retry or refuse. | `path` (string), `expected_sha256` (string), `actual_sha256` (string) |
| `mmap` | Loading the file into the engine via FFI. | `path` (string) |
| `kv_cache` | Allocating the KV cache. | `n_ctx` (int) |

Frames during one boot, in order, look approximately like:

```json
{"id":"admin","type":"status","status":"starting"}
{"id":"admin","type":"status","status":"loading_model","phase":"checking_local","detail":{"path":"/home/u/.inferd/models/gemma-4-e4b-ud-q4-k-xl.gguf"}}
{"id":"admin","type":"status","status":"loading_model","phase":"download","detail":{"downloaded_bytes":33554432,"total_bytes":5126304928,"source_url":"https://huggingface.co/..."}}
{"id":"admin","type":"status","status":"loading_model","phase":"download","detail":{"downloaded_bytes":67108864,"total_bytes":5126304928,"source_url":"https://huggingface.co/..."}}
... (~150 more progress frames during a 5 GB download)
{"id":"admin","type":"status","status":"loading_model","phase":"verify","detail":{"path":"/home/u/.inferd/models/gemma-4-e4b-ud-q4-k-xl.gguf"}}
{"id":"admin","type":"status","status":"loading_model","phase":"mmap","detail":{"path":"/home/u/.inferd/models/gemma-4-e4b-ud-q4-k-xl.gguf"}}
{"id":"admin","type":"status","status":"loading_model","phase":"kv_cache","detail":{"n_ctx":8192}}
{"id":"admin","type":"status","status":"ready"}
```

### 5.2 Forward compatibility

- Clients MUST ignore unknown `status` values, treating them as
  opaque (display them, log them, but do not branch on them).
- Clients MUST ignore unknown `phase` values within
  `loading_model`.
- Clients MUST ignore unknown fields within `detail`.
- The daemon WILL NOT introduce a new `status` or `phase` that
  breaks the existing semantics. Backwards-additive only.

> **v2 observation (inferd ≥ v0.5).** The unified-wire daemon
> leads its on-connect snapshot with one **`status: "capabilities"`**
> frame *per loaded backend* (e.g. `embeddinggemma-300m`, then
> `gemma-4-e4b`), each carrying `backend`, `vision`, `audio`,
> `tools`, `wire_version`, etc., *before* the lifecycle frame
> (`ready` / `loading_model` / …). `capabilities` is **not** a
> lifecycle state — per the rule above, a readiness client must
> treat it as opaque and keep reading until it sees a real
> lifecycle `status`. thlibo's probe (`parseAdminSnapshot` in
> `internal/install/inferd.go`) parses *all* snapshot frames and
> reports the last lifecycle status, skipping `capabilities`. A
> bug where it reported only the first frame surfaced as the
> spurious install note "inferd is capabilities; thlibo will fail
> open until ready" — fixed with a regression test pinned to the
> real captured bytes.

## 6. Idiomatic client patterns

### 6.1 Wait for ready (the simple case)

A middleware that just wants to know "can I send inference
requests now":

```
connect to admin socket
loop:
    read one NDJSON frame
    parse the JSON
    if parsed.status == "ready":
        break
    (otherwise: ignore; loop)
close admin socket
... now send inference traffic
```

Total client code: ~30 lines including connect-retry.

### 6.2 Display progress (installer GUI, dashboard)

```
connect to admin socket
loop:
    read one NDJSON frame
    parse the JSON
    if parsed.status == "loading_model" and parsed.phase == "download":
        show progress bar with parsed.detail.downloaded_bytes / parsed.detail.total_bytes
    elif parsed.status == "ready":
        show "ready"
        stop refreshing progress
    elif parsed.status == "draining":
        show "shutting down"
        break
```

### 6.3 The "passive readiness" alternative

Clients that don't need progress UX can skip the admin socket
entirely and use connect-and-retry against the inference socket.
The inference socket only exists when ready, so a successful
connect is equivalent to receiving a `ready` event. See §"Client
connection lifecycle" in `docs/protocol-v1.md`.

Most middlewares should pick one or the other based on whether
they care about progress, not both.

**Thlibo follows §6.3** for inference dispatch (per ADR 0006).
The admin socket is consumed only by `thlibo doctor` for
progress UX.

### 6.4 What to do during `restarting`

If a client is connected to the admin socket and sees
`restarting`:

- The inference socket has closed. Existing inference connections
  have already received EOF.
- New inference connections will fail with `ECONNREFUSED` until a
  subsequent `ready` event.
- The client should not disconnect from the admin socket — stay
  connected to learn when `ready` returns.
- Inference clients should reconnect (per §"Client connection
  lifecycle") once `ready` is observed.

## 7. Error semantics

The admin socket itself does not emit error frames in v1. It is a
status-broadcast channel; failures of the broadcast itself are
reflected in the connection (EOF) rather than in protocol frames.

Specifically:

- If the daemon crashes, the admin socket closes. Clients see
  EOF.
- If the daemon transitions to `draining` and then exits, clients
  see a `draining` frame followed by EOF.
- A client that takes too long reading frames and lets the
  broadcast queue overflow (default 256 frames) will be
  disconnected by the daemon (EOF). Reconnect to resume.

## 8. Versioning

This spec is part of inferd protocol v1. Versioning rules:

- v1 is immutable.
- New optional fields (within `detail`, new `phase` values, new
  `status` values) are backwards-additive and may land in any v1
  release.
- Breaking changes (renaming fields, removing fields, changing
  field types) require v2 on a new socket path:
  `/run/inferd/admin-v2.sock`, `\\.\pipe\inferd-admin-v2`.
  Clients that need v2 connect to the v2 path; the v1 path keeps
  working in parallel for the migration window.

There is no in-band version negotiation. Clients pick the socket
path matching the protocol version they speak.

## 9. Summary table for thlibo

| Question | Answer |
|----------|--------|
| Path on Linux | `/run/inferd/admin.sock` |
| Path on macOS | `${TMPDIR}/inferd/admin.sock` |
| Path on Windows | `\\.\pipe\inferd-admin` |
| Permission posture | 0600 UDS / current SID only on pipe; daemon-uid only |
| Frame format | NDJSON, one JSON object per `\n`-terminated line |
| Frame size cap | 64 MiB per frame |
| Direction | Daemon → client only in v1 (read-only socket) |
| Connect behaviour | Daemon writes current-state snapshot immediately on connect |
| Frame envelope | `{"id":"admin","type":"status","status":..., ...}` |
| Lifecycle states | `starting`, `loading_model`, `ready`, `restarting`, `draining` |
| `loading_model` phases | `checking_local`, `download`, `verify`, `quarantine`, `mmap`, `kv_cache` |
| Forward compat | Clients ignore unknown `status`, `phase`, and `detail` keys |
| Versioning | Part of protocol v1 (inferd ADR 0008); immutable |

---

## Thlibo integration notes (not part of the spec)

- Thlibo uses **§6.3 passive readiness** for the prompt-processor
  dispatch path. Connect-and-retry against the inference socket;
  passthrough on connect failure. See ADR 0006.
- Thlibo uses **§6.2 progress display** in `thlibo doctor` and
  the `/caselog` skill so users can see why compression isn't
  active during the bootstrap window.
- Inference dispatch path does NOT consult the admin socket.
  Keeping the critical path simple was an explicit ADR 0006
  decision.
