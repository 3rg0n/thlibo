# 0003. Per-user autostart, not a system service

- Status: accepted
- Date: 2026-05-12

## Context

`thlibod` should start automatically when the user logs in so that
AI client tool calls don't break on a missing daemon. The obvious
candidates are:

1. **System service** (Windows Service, launchd LaunchDaemon,
   systemd system unit). Most "professional" feel; runs
   independent of login.
2. **Per-user autostart** (Windows Startup folder, LaunchAgent
   with `RunAtLoad`, systemd user unit with
   `WantedBy=default.target`). Runs as the user, at login, with
   no admin privileges.

The security context matters more than either convenience
argument. On Windows we chose a pipe ACL that grants access to
the current user's SID only — a `LocalSystem` service would need
to run under that ACL, and the user's process couldn't connect.
On Unix the symmetric issue exists with socket ownership vs.
connecting-user's uid/gid. Rolling a shared group
(`thlibo-users`) would solve it but requires `sudo` at install,
which violates the "no elevation" property we wanted to keep.

## Decision

All three platforms use per-user autostart:

- **Windows**: `.cmd` shim in `%APPDATA%\Microsoft\Windows\
  Start Menu\Programs\Startup\`. Launches `thlibod.exe` via
  `start "" /B` so no console window appears.
- **macOS**: `~/Library/LaunchAgents/cisco.thlibo.daemon.plist`
  with `RunAtLoad: true`, activated via `launchctl bootstrap
  gui/$UID`.
- **Linux**: `~/.config/systemd/user/cisco.thlibo.daemon.service`
  with `WantedBy=default.target`, enabled via `systemctl --user
  enable --now`.

Each backend implements a common `install.Installer` interface
(Install / Uninstall / Status / Mechanism) so the installer
doesn't need to know which platform it's on.

## Consequences

**Easier:**

- No elevation required. `thlibo install` runs entirely in the
  user's home directory, no `sudo`, no UAC prompts, no password
  dialogs.
- The daemon's uid matches the user whose AI client is making
  requests — socket/pipe ACLs work out of the box.
- Consistent mental model across macOS, Linux, Windows: "runs
  when I log in, stops when I log out."

**Harder:**

- Multi-user machines: each user runs their own daemon. Memory
  cost scales with active logged-in users. Acceptable — thlibo
  is targeted at individual developer workstations, not shared
  build hosts.
- Headless servers / CI without a logged-in user don't benefit
  from autostart. Operators running thlibo on those machines
  wire `thlibod` into their own orchestration (systemd system
  unit, k8s Deployment, etc.) rather than using the installer.
- The E1 gate row (create `thlibo-users` group, add current
  user) is not needed. Struck through in the release gate with
  a pointer to this ADR.

## References

- Spec: `.plan/thlibo-spec.md` §Install flow
- Implementation: `internal/install/autostart*.go`
- Release gate: `.plan/release-gate.md` rows E1 (struck) and E2 (closed)
