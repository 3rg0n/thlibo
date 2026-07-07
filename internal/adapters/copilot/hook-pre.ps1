# thlibo-hook-version: 2
# thlibo preToolUse hook (Windows) -- GitHub Copilot CLI AND VS Code
# Copilot (1.111+, currently Insiders).
#
# PowerShell equivalent of hook-pre.sh. Detects which host's envelope is
# on stdin and replies in the matching wire format:
#
#   Copilot CLI   : { toolName, toolArgs:"<json-string>" }  (double-encoded!)
#                   -> { permissionDecision:"allow", modifiedArgs:{obj} }
#   VS Code/Claude: { tool_name, tool_input:{command|commandLine} }
#                   -> { hookSpecificOutput:{ hookEventName, permissionDecision,
#                                             updatedInput } }
#
# CRITICAL -- Copilot CLI preToolUse is FAIL-CLOSED (a non-zero exit
# denies the tool call) and VS Code treats exit 2 as blocking. This
# script exits 0 on EVERY path and only ever emits "allow". Never denies.
#
# Requires: thlibo.exe on PATH. Uses ConvertFrom/To-Json (no jq).

$ErrorActionPreference = 'SilentlyContinue'

try {
    $disabled = $env:THLIBO_DISABLED
    if ($disabled -eq '1' -or $disabled -eq 'true' -or $disabled -eq 'on' -or $disabled -eq 'yes') {
        exit 0
    }

    $thlibo = Get-Command thlibo -ErrorAction SilentlyContinue
    if (-not $thlibo) {
        [Console]::Error.WriteLine('[thlibo] WARNING: thlibo not on PATH; preToolUse hook disabled.')
        exit 0
    }

    $raw = [Console]::In.ReadToEnd()
    if (-not $raw) { exit 0 }
    $obj = $raw | ConvertFrom-Json

    # Detect the envelope: CLI carries toolArgs; VS Code/Claude carry
    # tool_input. NOTE: not $args -- that's a PowerShell automatic var.
    $isCli = $null -ne $obj.PSObject.Properties['toolArgs']
    if ($isCli) { $targs = $obj.toolArgs } else { $targs = $obj.tool_input }
    if (-not $targs) { exit 0 }

    # Ground truth from a live Copilot CLI (1.0.x): toolArgs arrives as a
    # JSON-ENCODED STRING (e.g. "{\"command\":\"git status\"}"), not a
    # nested object, so $targs.command would be null. Reparse a string.
    # tool_input (VS Code/Claude) is a real object and is left as-is.
    if ($targs -is [string]) { $targs = $targs | ConvertFrom-Json }
    if (-not $targs) { exit 0 }

    # Probe the command field (naming varies across tools/hosts).
    $cmd = $targs.command
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $targs.commandLine }
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $targs.cmd }
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $targs.script }
    if ([string]::IsNullOrEmpty($cmd)) { exit 0 }

    $rewritten = & thlibo rewrite $cmd 2>$null
    $exitCode  = $LASTEXITCODE
    if ($exitCode -ne 0 -or [string]::IsNullOrEmpty($rewritten) -or $rewritten -eq $cmd) {
        exit 0
    }
    $rewritten = $rewritten.TrimEnd("`r", "`n")

    # Swap whichever command key was present on the original input object.
    if ($null -ne $targs.PSObject.Properties['command'])     { $targs.command     = $rewritten }
    if ($null -ne $targs.PSObject.Properties['commandLine']) { $targs.commandLine = $rewritten }
    if ($null -ne $targs.PSObject.Properties['cmd'])         { $targs.cmd         = $rewritten }
    if ($null -ne $targs.PSObject.Properties['script'])      { $targs.script      = $rewritten }

    if ($isCli) {
        $out = [ordered]@{
            permissionDecision       = 'allow'
            permissionDecisionReason = 'thlibo auto-rewrite (output compression)'
            modifiedArgs             = $targs
        }
    } else {
        $out = [ordered]@{
            hookSpecificOutput = [ordered]@{
                hookEventName            = 'PreToolUse'
                permissionDecision       = 'allow'
                permissionDecisionReason = 'thlibo auto-rewrite (output compression)'
                updatedInput             = $targs
            }
        }
    }
    $out | ConvertTo-Json -Compress -Depth 10
    exit 0
} catch {
    # Fail-closed host: swallow everything, let the tool run unchanged.
    exit 0
}
