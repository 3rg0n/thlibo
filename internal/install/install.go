// Package install implements `thlibo install`: create the thlibo-users
// group, register the daemon as a service (systemd/launchd/Windows
// Service), prepare the socket directory, populate ~/.thlibo/processors/
// with on-disk copies of built-ins, and merge the PostToolUse hook into
// ~/.claude/settings.json without clobbering existing hooks.
package install
