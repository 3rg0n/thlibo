# thlibo-hook-version: 1
# thlibo Claude Code PreToolUse hook for the PowerShell tool.
#
# PowerShell equivalent of hook.sh. Reads the tool envelope from
# stdin, extracts tool_input.command, asks `thlibo rewrite` whether
# to wrap it, and emits the Claude Code hookSpecificOutput JSON.
#
# Exit-code protocol from thlibo rewrite is identical to the Bash
# variant; see hook.sh for the full table.
#
# Requires: thlibo.exe on PATH. Does NOT require jq — uses
# ConvertFrom-Json / ConvertTo-Json.

$ErrorActionPreference = 'SilentlyContinue'

# UTF-8 on every encoding this hook touches. PowerShell 5.1 -- the
# `powershell` this hook is registered under -- defaults all three to a
# non-UTF-8 code page while the envelope is UTF-8: [Console]::InputEncoding
# decodes stdin (the OEM page, IBM437 on a default box),
# [Console]::OutputEncoding decodes a child process's stdout (same page),
# and $OutputEncoding encodes what we pipe to a child (ASCII). Measured
# before this was set: `thlibo rewrite` returned an accented character in
# the command as two box-drawing characters, and Claude Code then ran that
# corrupted command. Assigning [Console]::OutputEncoding calls
# SetConsoleOutputCP, which throws when no console is attached, so it
# degrades on its own rather than taking the hook down (#134).
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch { }

# Per-session kill switch. Users who want to bypass thlibo for one
# session set $env:THLIBO_DISABLED=1 without needing to uninstall.
# See THREAT_MODEL.md finding #16.
$disabled = $env:THLIBO_DISABLED
if ($disabled -eq '1' -or $disabled -eq 'true' -or $disabled -eq 'on' -or $disabled -eq 'yes') {
    exit 0
}

# If thlibo isn't on PATH, bail out cleanly so Claude Code proceeds.
$thlibo = Get-Command thlibo -ErrorAction SilentlyContinue
if (-not $thlibo) {
    [Console]::Error.WriteLine('[thlibo] WARNING: thlibo not on PATH; hook disabled.')
    exit 0
}

# Slurp stdin. PowerShell's $input is a pipeline; read it all. Use an
# explicit UTF-8 reader rather than [Console]::In, which decodes with
# [Console]::InputEncoding -- the OEM page (#134).
$stdinReader = [System.IO.StreamReader]::new(
    [Console]::OpenStandardInput(), [System.Text.UTF8Encoding]::new($false))
$raw = $stdinReader.ReadToEnd()
if (-not $raw) { exit 0 }

try {
    $obj = $raw | ConvertFrom-Json
} catch {
    # Malformed envelope — defer to Claude Code.
    exit 0
}

$cmd = $obj.tool_input.command
if ([string]::IsNullOrEmpty($cmd)) { exit 0 }

# Invoke thlibo rewrite. Capture stdout and exit code separately so
# we can apply the exit-code protocol.
$rewritten = & thlibo rewrite $cmd 2>$null
$exitCode  = $LASTEXITCODE

switch ($exitCode) {
    0 {
        if ([string]::IsNullOrEmpty($rewritten) -or $rewritten -eq $cmd) { exit 0 }
    }
    3 {
        if ([string]::IsNullOrEmpty($rewritten)) { exit 0 }
    }
    default {
        # Passthrough on any other exit code (1, 2, internal error).
        exit 0
    }
}

# Strip any trailing newline so the JSON stays tight.
$rewritten = $rewritten.TrimEnd("`r", "`n")

# Build the updatedInput by mutating a copy of tool_input.
$obj.tool_input.command = $rewritten

$out = [ordered]@{
    hookSpecificOutput = [ordered]@{
        hookEventName = 'PreToolUse'
        updatedInput  = $obj.tool_input
    }
}
if ($exitCode -eq 0) {
    $out.hookSpecificOutput.permissionDecision       = 'allow'
    $out.hookSpecificOutput.permissionDecisionReason = 'thlibo auto-rewrite'
}

# -Compress keeps the line tight; -Depth 10 handles nested tool_input
# schemas without truncation.
$out | ConvertTo-Json -Compress -Depth 10
