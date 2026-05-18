# Linux runtime context for inferd

Handoff document for the inferd implementation. Captures everything
thlibo learned about running an inference daemon on Linux during
the v0.5.x lifecycle, so inferd doesn't repeat the same bugs.

This is a one-shot context dump. Once inferd v0.1.0 GA ships, this
document can be deleted from thlibo's repo.

---

## 1. Audience and scope

Audience: inferd maintainers / inferd-Claude.

Scope: Linux-specific lessons. Mac and Windows are working in
thlibo today; only Linux needed surgery.

Non-goals: telling inferd how to architect its daemon — inferd
already has its own ADRs. This is the "what bit us, don't do it
again" list.

---

## 2. What works on Linux today (v0.5.4)

**Install path is verified end-to-end on Ubuntu 26.04 / WSL2:**

- Tarball download + SHA-256 verify
- Binary extraction to `~/.local/bin/{thlibo,thlibod,thlibo-engine}`
- Built-in processor mirror to `~/.thlibo/processors/`
- Hook scripts written to `~/.thlibo/hooks/{thlibo-rewrite,thlibo-read,thlibo-write}.{sh,ps1}`
- `~/.claude/settings.json` merge (Bash + PowerShell + Read + Write/Edit matchers)
- systemd `--user` unit at `~/.config/systemd/user/cisco.thlibo.daemon.service`
- Engine binary download (878 MB)
- Model GGUF download (5.1 GB) to `~/.thlibo/models/`

**Daemon startup (post-v0.5.4) reaches engine spawn:**

- Lock file binds at `$XDG_RUNTIME_DIR/thlibo/thlibod.lock`
- Sockets bind at `$XDG_RUNTIME_DIR/thlibo/{infer,admin}.sock`
- systemd unit declares `RuntimeDirectory=thlibo` so the dir is
  auto-created with the right perms before ExecStart

---

## 3. The four Linux-specific bugs we hit

### 3.1 Hard-coded `/run/<service>/` path

**Symptom:**
```
thlibod: daemon: create lock dir: mkdir /run/thlibo: read-only file system
status=4/NOPERMISSION
```

**Root cause:** `/run/<service>/` is for system daemons running as
root. A `systemd --user` unit cannot mkdir there. `/run/user/<uid>/`
is the per-user equivalent and is provisioned by `systemd-logind`
on session start.

**inferd should:**

- Use `$XDG_RUNTIME_DIR` for the inference socket, the admin
  socket, and the lock file on Linux. Falls back to
  `$HOME/.<name>/run` or `$TMPDIR/<name>` if the env var is
  unset (containers, ssh sessions without logind, etc.).
- Document this in the protocol spec — thlibo's
  [`docs/inferd-admin-protocol-v1.md`](../docs/inferd-admin-protocol-v1.md) §2 already
  lists `/run/inferd/admin.sock` as the Linux path. **Update that
  to `$XDG_RUNTIME_DIR/inferd/admin.sock`** before v0.1.0 freezes,
  or thlibo's client will need a fallback resolution chain that
  duplicates the daemon's logic.
- Consider whether the protocol spec should freeze the *resolution
  algorithm* rather than a single literal path. A spec that says
  "compute path via this algorithm" rules out a class of
  per-distro divergence.

### 3.2 systemd `ProtectSystem=strict` blocks `$XDG_RUNTIME_DIR` writes

**Symptom (after fixing 3.1):**
```
thlibod: daemon: create lock dir: mkdir /run/user/1000/thlibo: read-only file system
```

**Root cause:** the unit's hardening directives default the entire
filesystem to read-only. Even paths the user *could* write to are
re-mounted read-only inside the unit's namespace. The fix is to
declare the runtime directory explicitly:

```
RuntimeDirectory=inferd
RuntimeDirectoryMode=0700
```

systemd then auto-creates `/run/user/<uid>/inferd/` with the
correct owner before ExecStart and tears it down after the
service stops. Compatible with `ProtectSystem=strict`.

**inferd should:**

- Ship a systemd unit template that includes both directives.
- Match thlibo's hardening posture: `NoNewPrivileges=true`,
  `PrivateDevices=true`, `PrivateTmp=true`, `ProtectSystem=strict`,
  `ProtectHome=read-only`, `ReadWritePaths=%h/.inferd`.
- Add `RestartSec=2` and `StartLimitBurst=3` /
  `StartLimitIntervalSec=60` to prevent crash-loop floods (we
  hit this; systemd's default is unlimited).
- Avoid `PrivateTmp=true` if you ever need to share `/tmp` with
  another process on the host (we don't, but it's a foot-gun if
  inferd ever needs to publish anything to `/tmp/<x>/...` for
  external consumers).

### 3.3 LaunchAgent / systemd unit fires before downloads finish

**Symptom (macOS, also applies to Linux):** the autostart entry
gets registered while `thlibo install --pull-engine --pull-model`
is still downloading. The supervisor fires immediately, the
daemon panics on the missing engine + model, and crash-loops to
the restart limit before the downloads finish.

**Fix landed in v0.5.4** (Mac Claude's `971a9b7`, kept for v0.6.0):

1. Register the autostart entry **after** all downloads complete.
2. If downloads are interrupted and the supervisor still fires,
   the daemon does a preflight wait loop: sleeps 30s and retries
   for up to 5 minutes. Logs `waiting_for_assets` so the operator
   knows why it's hanging.

**inferd should:**

- Order the install steps so the autostart unit is registered
  last, after the engine + model are on disk.
- Consider whether the daemon should refuse to start without
  required assets, or wait for them. Thlibo chose "wait + log"
  because it's gentler on the operator. Either is defensible.

### 3.4 llamafile `--host <unix-socket>` does not bind on Linux

**Symptom (after fixing 3.1, 3.2, 3.3):**
```
start: setting address family to AF_UNIX
start: couldn't bind HTTP server socket, hostname: /tmp/thlibo/llamafile-79764.sock
srv operator(): cleaning up before exit...
server_main: exiting due to HTTP server error
thlibod: daemon: engine exited before ready
```

**Root cause:** llamafile v0.10.1 enters `AF_UNIX` mode correctly
when given a path-shaped `--host`, but `bind(2)` fails silently.
Path is well under 108 bytes, parent dir exists with correct
perms, EACCES isn't the issue. Reproduces outside systemd. TCP
mode on the same engine binary works fine.

This is upstream llamafile, not thlibo. We didn't fix it in
v0.5.4 because v0.6.0 + inferd makes it disappear.

**inferd should NOT use llamafile.** The protocol spec already
says inferd vendors `llama.cpp` directly via FFI in
`inferd-engine` — that's the correct call. llamafile is APE
(Cosmopolitan-Libc) which has its own portability tradeoffs (see
3.5 below) and the `--host` flag has at least this one
Linux-specific bug.

**If inferd ever needs an HTTP-server-style sidecar engine** for
some reason (it shouldn't, but in case): use loopback TCP, not
UDS. Add a per-process shared-secret header to the requests so
arbitrary local processes can't talk to the engine. UDS gives
you free same-user-only auth via filesystem permissions; TCP
loopback gives you any-process-on-host reachability that needs
auth at the protocol layer.

### 3.5 WSL's binfmt_misc handler hijacks APE binaries

**Symptom (only on WSL):**
```
error: APE is running on WIN32 inside WSL.
You need to run: sudo sh -c 'echo -1 > /proc/sys/fs/binfmt_misc/WSLInterop'
```

**Root cause:** WSL registers a binfmt_misc handler that matches
on the `MZ` magic at offset 0 of executables. The intent is "run
Windows .exe files from inside WSL by routing them through the
Windows host." APE / Cosmopolitan-Libc binaries are deliberately
polyglot — `MZ` header for Windows compat + ELF body for Linux —
so WSL grabs them and tries to run them as Windows binaries.

**Fixes (per-user, choose one):**

- Per-boot: `sudo sh -c 'echo -1 > /proc/sys/fs/binfmt_misc/WSLInterop'`
- Permanent: `[interop] enabled = false` in `/etc/wsl.conf` then
  `wsl.exe --shutdown`

**v0.5.4 added `wslAPEInteropHint()`** in
`cmd/thlibo/installcmd/install.go` — detects WSL via
`/proc/sys/fs/binfmt_misc/WSLInterop` + `/proc/version` contents
and prints a one-time advisory after install. The fix itself is
manual on the user's part (we don't have sudo).

**inferd inherits this for free** because it doesn't ship an APE
binary — `inferd-engine` will be a normal ELF compiled against
vendored `llama.cpp`, no Cosmopolitan, no `MZ` header. WSL won't
try to hijack it.

**Recommendation:** mention in inferd's README's Linux section
that WSL users with thlibo's old llamafile engine on PATH should
remove it / let inferd's installer overwrite it. Old polyglot
binary lying around could still trip the WSLInterop handler if
something tries to exec it.

---

## 4. Linux-specific path table for inferd

Mirror of thlibo's table, with `/inferd/` substituted:

| Asset | Path (Linux) | Owner | Note |
|-------|--------------|-------|------|
| Inference socket | `${XDG_RUNTIME_DIR:-$HOME/.inferd/run}/inferd/infer.sock` | daemon uid | Spec freeze candidate |
| Admin socket | `${XDG_RUNTIME_DIR:-$HOME/.inferd/run}/inferd/admin.sock` | daemon uid | Spec freeze candidate |
| Lock file | `${XDG_RUNTIME_DIR:-$HOME/.inferd/run}/inferd/inferd.lock` | daemon uid | |
| Model store | `${XDG_DATA_HOME:-$HOME/.local/share}/inferd/models/` | daemon uid | See [`.plan/spec.issue.md`](spec.issue.md) for the shared-store proposal |
| Config | `${XDG_CONFIG_HOME:-$HOME/.config}/inferd/config.yaml` | daemon uid | |
| Cache | `${XDG_CACHE_HOME:-$HOME/.cache}/inferd/` | daemon uid | KV cache spillover, etc. |
| State / logs | `${XDG_STATE_HOME:-$HOME/.local/state}/inferd/` | daemon uid | |
| systemd unit | `~/.config/systemd/user/cisco.inferd.daemon.service` | the user | Match thlibo's naming if you want to share autostart-disable scripts |

The `cisco.inferd.daemon` prefix is open for debate — we used
`cisco.thlibo.daemon` because of the work-context. Inferd is open
source and probably wants something neutral like `inferd.daemon`
or `dev.inferd.daemon`. Doesn't matter for correctness.

---

## 5. Failure modes inferd should expose to clients

These all manifested as silent passthrough in thlibo. Without an
admin socket they were invisible to the user.

| Failure | Inferd admin status | thlibo client behaviour |
|---------|---------------------|-------------------------|
| Lock dir mkdir fails | Daemon can't even start; admin socket never binds | Connect refused → passthrough |
| systemd PrivateTmp blocks engine socket | Daemon process up but engine never reaches ready | `loading_model` phase hung indefinitely → passthrough after timeout |
| Model file missing | `loading_model phase=checking_local` then crash | Connect refused on infer socket → passthrough |
| Model SHA mismatch | `loading_model phase=quarantine` | Connect refused → passthrough |
| Engine bind fails | Daemon up, model ready, engine fails to expose endpoint | Connect succeeds on admin, infer socket never binds → passthrough |
| WSL APE hijack | Engine subprocess exits before listen | Same as above |

All of these are inferd's problem to surface via the admin
socket. Thlibo only consumes:

1. Connect-success on the inference socket = ready (per ADR 0006
   passive readiness)
2. Connect-fail = passthrough

The admin socket is for `thlibo doctor`'s "why isn't compression
working?" diagnostic — that's where inferd's rich lifecycle
states actually pay off. None of them gate the inference dispatch
path.

---

## 6. Test fixtures that would have caught this

If inferd's CI matrix can run an Ubuntu 26.04 / WSL2 job that:

1. Installs the inferd binary
2. Drops in a stub model + engine
3. Starts the systemd unit
4. Connects to the admin socket and waits for `ready`
5. Sends one inference request
6. Asserts non-passthrough response

…it will catch every issue in §3 except 3.4 (llamafile-specific,
not applicable to inferd) and 3.5 (WSL-specific, would need a
WSL CI runner — GitHub doesn't provide one yet, but
[Microsoft's wsl-2 action](https://github.com/Vampire/setup-wsl)
on a Windows runner works).

Thlibo did not have this CI coverage. We caught the bugs by
running the released binary on a real WSL Ubuntu and reading the
journal. Worth doing better in inferd from day one.

---

## 7. Single takeaway

**Per-user systemd units are the path of least resistance on
Linux, but only if you respect `$XDG_RUNTIME_DIR` and declare
`RuntimeDirectory=` in the unit.** Everything else flows from
that. The protocol spec freeze is the right place to bake those
paths in.
