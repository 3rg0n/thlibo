// Package install implements `thlibo install`: register the daemon
// for per-user autostart (systemd --user on Linux, launchd LaunchAgent
// on macOS, Startup-folder shim on Windows), prepare the socket
// directory, populate ~/.thlibo/processors/ with on-disk copies of
// built-ins, and merge the PreToolUse hook into ~/.claude/settings.json
// without clobbering existing hooks.
//
// Group creation note: v0.1 runs entirely per-user (ADR 0002, 0003).
// The `thlibo-users` group referenced by the Unix socket ACL is NOT
// created by `thlibo install` — doing so would require root. If an
// operator wants multi-user daemon sharing on Unix, they create the
// group out-of-band (`sudo groupadd thlibo-users && sudo usermod -aG
// thlibo-users $USER`) and add each consumer. Without the group, the
// chown is a no-op and the socket is owned by the daemon's primary
// group, which is correct for single-user installs.
package install
