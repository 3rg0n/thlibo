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

# If thlibo isn't on PATH, bail out cleanly so Claude Code proceeds.
$thlibo = Get-Command thlibo -ErrorAction SilentlyContinue
if (-not $thlibo) {
    [Console]::Error.WriteLine('[thlibo] WARNING: thlibo not on PATH; hook disabled.')
    exit 0
}

# Slurp stdin. PowerShell's $input is a pipeline; read it all.
$raw = [Console]::In.ReadToEnd()
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
