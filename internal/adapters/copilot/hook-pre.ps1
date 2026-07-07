# thlibo-hook-version: 1
# thlibo GitHub Copilot CLI preToolUse hook for the shell tool (Windows).
#
# PowerShell equivalent of hook-pre.sh. Copilot's config.toml-style
# hooks file carries a "powershell" command per entry, so on Windows
# Copilot runs THIS script natively -- no bash wrapping needed.
#
# CRITICAL -- Copilot preToolUse is FAIL-CLOSED: a non-zero exit denies
# the tool call and breaks the client. This script exits 0 on EVERY
# path and only ever emits permissionDecision "allow". It never denies.
#
# stdin:  { sessionId,timestamp,cwd,toolName, toolArgs:{...} }
# stdout: { permissionDecision:"allow", modifiedArgs:{...} }
#
# Requires: thlibo.exe on PATH. Uses ConvertFrom/To-Json (no jq).

$ErrorActionPreference = 'SilentlyContinue'

# Fail-closed backstop: wrap the whole body so any thrown error still
# exits 0 (Copilot proceeds with the original command).
try {
    $disabled = $env:THLIBO_DISABLED
    if ($disabled -eq '1' -or $disabled -eq 'true' -or $disabled -eq 'on' -or $disabled -eq 'yes') {
        exit 0
    }

    $thlibo = Get-Command thlibo -ErrorAction SilentlyContinue
    if (-not $thlibo) {
        [Console]::Error.WriteLine('[thlibo] WARNING: thlibo not on PATH; Copilot preToolUse hook disabled.')
        exit 0
    }

    $raw = [Console]::In.ReadToEnd()
    if (-not $raw) { exit 0 }

    $obj = $raw | ConvertFrom-Json
    # NOTE: not $args -- that's a PowerShell automatic variable; use a
    # distinct name so a future refactor into a function can't shadow it.
    $targs = $obj.toolArgs
    if (-not $targs) { exit 0 }

    # Probe the command field (docs type toolArgs as unknown).
    $cmd = $targs.command
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $targs.cmd }
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $targs.script }
    if ([string]::IsNullOrEmpty($cmd)) { exit 0 }

    $rewritten = & thlibo rewrite $cmd 2>$null
    $exitCode  = $LASTEXITCODE

    # Only exit 0 = "wrap it". Anything else is a passthrough; on a
    # fail-closed host we never turn a deny into a Copilot deny.
    if ($exitCode -ne 0 -or [string]::IsNullOrEmpty($rewritten) -or $rewritten -eq $cmd) {
        exit 0
    }
    $rewritten = $rewritten.TrimEnd("`r", "`n")

    # Replace whichever command key was present on the original args.
    if ($null -ne $targs.PSObject.Properties['command']) { $targs.command = $rewritten }
    if ($null -ne $targs.PSObject.Properties['cmd'])     { $targs.cmd     = $rewritten }
    if ($null -ne $targs.PSObject.Properties['script'])  { $targs.script  = $rewritten }

    $out = [ordered]@{
        permissionDecision       = 'allow'
        permissionDecisionReason = 'thlibo auto-rewrite (output compression)'
        modifiedArgs             = $targs
    }
    $out | ConvertTo-Json -Compress -Depth 10
    exit 0
} catch {
    # Fail-closed host: swallow everything, let the tool run unchanged.
    exit 0
}
