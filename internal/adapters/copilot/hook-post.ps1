# thlibo-hook-version: 1
# thlibo GitHub Copilot CLI postToolUse hook (Windows).
#
# PowerShell equivalent of hook-post.sh. Copilot runs this natively on
# Windows via the hook entry's "powershell" command.
#
# Replaces the tool result with a compressed version via modifiedResult:
#   { modifiedResult: { resultType:"success", textResultForLlm:"<c>" } }
#
# postToolUse is fail-open, so errors can't break the client; we still
# exit 0 on every passthrough to keep the log quiet.
#
# Double-compression guard: if preToolUse already wrapped the command
# as `<thlibo> exec -- ...`, the output was already compressed inside
# `thlibo exec`; re-compressing could mangle it, so we pass through.
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
        [Console]::Error.WriteLine('[thlibo] WARNING: thlibo not on PATH; Copilot postToolUse hook disabled.')
        exit 0
    }

    $raw = [Console]::In.ReadToEnd()
    if (-not $raw) { exit 0 }

    $obj = $raw | ConvertFrom-Json

    # Double-compression guard: skip our own preToolUse-wrapped commands.
    $cmd = $obj.toolArgs.command
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $obj.toolArgs.cmd }
    if ([string]::IsNullOrEmpty($cmd)) { $cmd = $obj.toolArgs.script }
    if ($cmd -like '*exec -- *' -or $cmd -like '*thlibo exec*') { exit 0 }

    # Model-visible output.
    $output = $obj.toolResult.textResultForLlm
    if ([string]::IsNullOrEmpty($output)) { $output = $obj.toolResult.output }
    if ([string]::IsNullOrEmpty($output) -and $obj.toolResult -is [string]) { $output = $obj.toolResult }
    if ([string]::IsNullOrEmpty($output)) { exit 0 }

    # Mirror the middleware's 2000-BYTE short-circuit. Measure UTF-8
    # bytes, not .Length (which is UTF-16 char count) so multibyte
    # output isn't under-counted and wrongly skipped.
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
