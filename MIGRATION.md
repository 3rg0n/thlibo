# v0.6.0 — inferd extraction

> **This branch does not compile.** That's intentional. See below.

## What is this branch

`v0.6.0` is the long-lived feature branch tracking the extraction
of inference into the separate `inferd` service per ADR 0005 and
ADR 0006. Bug fixes that land on `main` between now and inferd
v0.1.0 GA are cherry-picked into this branch periodically. When
inferd ships and its Go client module is published, this branch
is finished, PR'd, and merged into `main` as v0.6.0.

## Why it doesn't compile

The first commit on this branch deletes ~6,150 lines of code that
move out of thlibo and into inferd:

- `cmd/thlibod/` — the embedded daemon binary
- `internal/daemon/` — daemon lifecycle, engine supervisor, llamafile spawner, lock, server-side peer-cred
- `internal/queue/` — single-active admission queue
- `internal/ipc/` — NDJSON wire types, endpoint, peer-cred client
- `internal/router/` — daemon-side prompt routing
- `internal/install/model.go` + `engine.go` — model and engine download

Everything that imported those packages now has dangling imports.
`go build ./...` will fail loudly. CI on this branch is expected
to fail until the next phase.

## What still needs to happen

When inferd v0.1.0 GA ships and `github.com/3rg0n/inferd/clients/go`
is on go.pkg.dev:

1. **Add the inferd client dep**:
   ```
   go get github.com/3rg0n/inferd/clients/go@v0.1.0
   ```

2. **Rewire the importers** (broken right now):
   - `cmd/thlibo/execcmd/pipeline.go`
   - `cmd/thlibo/compresscmd/compress.go`
   - `cmd/thlibo/shorthandcmd/shorthand.go`
   - `internal/middleware/middleware.go`

   Each currently imports `internal/ipc` for request/response types
   and `internal/router` for the daemon round-trip. They should
   import `inferd "github.com/3rg0n/inferd/clients/go"` and call
   `inferd.DialUnix(ctx, ...)` + `client.Generate(ctx, req)`.
   Connect failure → passthrough per ADR 0006.

3. **Rewire the installer** (`cmd/thlibo/installcmd/install.go`):
   - Remove model + engine download (gone with `internal/install/model.go`).
   - Detect inferd on PATH or fetch it from `github.com/3rg0n/inferd/releases`.
   - Verify SHA-256 against inferd's `SHA256SUMS`.
   - Register inferd's autostart entry (or invoke inferd's installer).
   - Continue with thlibo's hook + processor mirroring.

4. **Repurpose autostart code** (`internal/install/autostart*.go`):
   - The systemd unit / LaunchAgent / Startup .cmd shim used to
     register `thlibod`. Now they register `inferd` (or are
     deleted entirely if inferd ships its own autostart logic
     and thlibo just invokes inferd's installer).

5. **Drop the model path migration code** that lives in
   `cmd/thlibo/main.go` (if added during the branch's lifetime):
   `~/.thlibo/models/...gguf` → `~/.inferd/models/...gguf`. Same
   pinned SHA. Operators with the file already on disk get a
   one-time `thlibo install --migrate-model` that symlinks (or
   moves) so they don't redownload 5 GB.

6. **Update the threat model** (`THREAT_MODEL.md`):
   - Findings about server-side peer-cred check, queue admission,
     llamafile sandboxing, model-fetch TLS — all become
     out-of-scope for thlibo (now inferd's responsibility). Replace
     with findings about the inferd client surface: connect
     timeout, passthrough on failure, no eval-gate-like surprises
     during bootstrap.

7. **Run lint + test sweep + scanner sweep**. Expect a clean run
   once all dangling imports are repaired.

8. **Update CHANGELOG**: flip `[Unreleased]` to
   `[0.6.0] - YYYY-MM-DD` with the full architectural-change
   summary linking ADRs 0005/0006.

## Cherry-picking from main

While this branch waits for inferd, main may receive bug fixes
that should also land in v0.6.0. Process:

```bash
git checkout v0.6.0
git fetch origin
git cherry-pick <sha-from-main>
# resolve conflicts (often skip changes to deleted files)
git push origin v0.6.0
```

Conflicts are common because we deleted files main still touches.
For changes to deleted files: skip the hunk (the work moved to
inferd; that fix happens in inferd's repo, not here). For changes
to surviving files: apply.

## Reference

- ADR 0005: [`docs/adr/0005-extract-inference-to-inferd.md`](docs/adr/0005-extract-inference-to-inferd.md)
- ADR 0006: [`docs/adr/0006-fail-open-during-inferd-bootstrap.md`](docs/adr/0006-fail-open-during-inferd-bootstrap.md)
- Vendored protocol spec: [`docs/inferd-admin-protocol-v1.md`](docs/inferd-admin-protocol-v1.md)
- Inferd repo: `github.com/3rg0n/inferd`
