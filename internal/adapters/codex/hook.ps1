# thlibo Codex PostToolUse hook (Windows).
#
# PowerShell equivalent of hook.sh, for the same reason the Claude Code
# adapter ships one (#127): on Windows a bare `.sh` path in a hook
# `command` goes through the .sh file association, which on a
# Git-for-Windows box is git-bash.exe — a terminal launcher, not an
# interpreter. The hook then opens a window and never feeds the tool.
# This script needs neither bash nor jq; it uses ConvertFrom/To-Json.
#
# The flow is hook.sh's:
#
#   1. Codex ran `git status`, captured its stdout.
#   2. PostToolUse fires, tool_response carries the output.
#   3. We pipe that output through `thlibo compress`.
#   4. We emit {"decision": "block", "reason": "<compressed>"}.
#   5. Codex substitutes the compressed string for the original tool
#      result in the model's context.
#
# Fail open on every path we don't understand: exit 0 with no stdout
# leaves the original tool result in place (invariant #2).
#
# Requires: thlibo.exe on PATH.
# Requires Codex feature flag: `[features] hooks = true` in
# ~/.codex/config.toml, plus a `/hooks` trust approval.

$ErrorActionPreference = 'SilentlyContinue'

# PowerShell 5.1 — the `powershell` this hook is registered under —
# defaults $OutputEncoding to ASCII, so piping to a native command
# replaces every non-ASCII character with "?". Measured: `2048 windows ×
# 256 dims` reached `thlibo compress` as `2048 windows ? 256 dims`, and
# the model then read the mangled text. Set UTF-8 in both directions.
# [Console]::OutputEncoding is the second half and not cosmetic — it also
# decodes a child process's stdout, so without it `thlibo compress`'s
# UTF-8 answer comes back through the OEM code page (#134).
# [Console]::OutputEncoding calls SetConsoleOutputCP, which throws when
# no console is attached, so it degrades on its own rather than taking
# the hook down.
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch { }

try {
    # Per-shell kill switch; see THREAT_MODEL.md finding #16.
    $disabled = $env:THLIBO_DISABLED
    if ($disabled -eq '1' -or $disabled -eq 'true' -or $disabled -eq 'on' -or $disabled -eq 'yes') {
        exit 0
    }

    $thlibo = Get-Command thlibo -ErrorAction SilentlyContinue
    if (-not $thlibo) {
        [Console]::Error.WriteLine('[thlibo] WARNING: thlibo not on PATH; Codex hook disabled.')
        exit 0
    }

    # Read stdin as UTF-8 explicitly. [Console]::In decodes with
    # [Console]::InputEncoding, which is the OEM code page on a default
    # Windows box, so the envelope's non-ASCII bytes would be lost before
    # anything else runs.
    $stdin = [System.IO.StreamReader]::new(
        [Console]::OpenStandardInput(), [System.Text.UTF8Encoding]::new($false))
    $raw = $stdin.ReadToEnd()
    if (-not $raw) { exit 0 }
    $obj = $raw | ConvertFrom-Json

    # Codex's Bash tool_response shape isn't explicitly documented (the
    # public docs point at the generated schema files). Look for the
    # common keys, then fall back to the whole tool_response as JSON —
    # the middleware handles whatever we feed it.
    $tr = $obj.tool_response
    if ($null -eq $tr) { exit 0 }
    if ($tr -is [string]) {
        $output = $tr
    } else {
        $output = $tr.output
        if ([string]::IsNullOrEmpty($output)) { $output = $tr.stdout }
        if ([string]::IsNullOrEmpty($output)) { $output = $tr.text }
        if ([string]::IsNullOrEmpty($output)) { $output = $tr | ConvertTo-Json -Compress -Depth 10 }
    }
    if ([string]::IsNullOrEmpty($output)) { exit 0 }

    # Mirror the middleware's 2000-BYTE short-circuit so small output
    # doesn't cost a subprocess. Measure UTF-8 bytes, not .Length
    # (UTF-16 char count), so multibyte output isn't under-counted.
    $outBytes = [System.Text.Encoding]::UTF8.GetByteCount($output)
    if ($outBytes -lt 2000) { exit 0 }

    $compressed = $output | & thlibo compress 2>$null
    if ($compressed -is [array]) { $compressed = $compressed -join "`n" }
    if ([string]::IsNullOrEmpty($compressed)) { exit 0 }

    # If compression didn't shrink anything (no matching processor, or
    # the pipeline fell through to passthrough), leave the original tool
    # result alone rather than reroute it through decision:block.
    $compBytes = [System.Text.Encoding]::UTF8.GetByteCount($compressed)
    if ($compBytes -ge $outBytes) { exit 0 }

    $out = [ordered]@{
        decision = 'block'
        reason   = $compressed
        hookSpecificOutput = [ordered]@{
            hookEventName     = 'PostToolUse'
            additionalContext = 'Tool output compressed by thlibo before the model read it.'
        }
    }
    $out | ConvertTo-Json -Compress -Depth 10
    exit 0
} catch {
    exit 0
}
