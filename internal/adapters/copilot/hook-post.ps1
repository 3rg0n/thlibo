# thlibo-hook-version: 2
# thlibo postToolUse hook (Windows) -- GitHub Copilot CLI (output
# replacement) and VS Code Copilot / Claude Code (observe-only; no-op).
#
# PowerShell equivalent of hook-post.sh. Only the Copilot CLI's
# postToolUse can REPLACE a tool's result (via modifiedResult); VS Code
# and Claude Code postToolUse are side-effect-only, so on those
# envelopes shell output is compressed by the preToolUse command-wrap
# (hook-pre.ps1) and this hook passes through.
#
# Envelope detection:
#   Copilot CLI    : { toolResult: { textResultForLlm } }  -> replace
#   VS Code/Claude : no toolResult                          -> no-op
#
# postToolUse is fail-open on all hosts; we still exit 0 on every
# passthrough. Double-compression guard skips already-`exec --`-wrapped
# commands.
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
        [Console]::Error.WriteLine('[thlibo] WARNING: thlibo not on PATH; postToolUse hook disabled.')
        exit 0
    }

    $raw = [Console]::In.ReadToEnd()
    if (-not $raw) { exit 0 }
    $obj = $raw | ConvertFrom-Json

    # Only the Copilot CLI envelope (toolResult) supports output
    # replacement. VS Code / Claude Code postToolUse is observe-only.
    if ($null -eq $obj.PSObject.Properties['toolResult']) { exit 0 }

    # Double-compression guard: skip our own preToolUse-wrapped commands.
    $cmd = $obj.toolArgs.command
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $obj.toolArgs.commandLine }
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $obj.toolArgs.cmd }
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $obj.toolArgs.script }
    if ($cmd -like '*exec -- *' -or $cmd -like '*thlibo exec*') { exit 0 }

    # Model-visible output.
    $output = $obj.toolResult.textResultForLlm
    if ([string]::IsNullOrEmpty($output)) { $output = $obj.toolResult.output }
    if ([string]::IsNullOrEmpty($output) -and $obj.toolResult -is [string]) { $output = $obj.toolResult }
    if ([string]::IsNullOrEmpty($output)) { exit 0 }

    # Mirror the middleware's 2000-BYTE short-circuit. Measure UTF-8
    # bytes, not .Length (UTF-16 char count), so multibyte output isn't
    # under-counted and wrongly skipped.
    $outBytes = [System.Text.Encoding]::UTF8.GetByteCount($output)
    if ($outBytes -lt 2000) { exit 0 }

    $compressed = $output | & thlibo compress 2>$null
    if ($compressed -is [array]) { $compressed = $compressed -join "`n" }
    if ([string]::IsNullOrEmpty($compressed)) { exit 0 }
    $compBytes = [System.Text.Encoding]::UTF8.GetByteCount($compressed)
    if ($compBytes -ge $outBytes) { exit 0 }

    $out = [ordered]@{
        modifiedResult = [ordered]@{
            resultType       = 'success'
            textResultForLlm = $compressed
        }
        additionalContext = 'Tool output compressed by thlibo before the model read it.'
    }
    $out | ConvertTo-Json -Compress -Depth 10
    exit 0
} catch {
    exit 0
}
