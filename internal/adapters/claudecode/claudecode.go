// Package claudecode adapts the thlibo middleware to Claude Code's
// PostToolUse hook: read stdin, invoke the middleware pipeline, write
// stdout, exit 0 even on internal errors (fallback contract).
package claudecode
