# thlibo-hook-version: 1
# thlibo Claude Code PreToolUse hook for the Read tool.
#
# When Claude reads a log-shaped file above a size threshold, build a
# thlibo case directory (compressed.log + summary.md + meta.json) and
# rewrite tool_input.file_path to the compressed variant so Claude
# sees the small version.
#
# Exit behaviour:
#   - match + success  -> emit hookSpecificOutput with updatedInput.file_path
#   - no match / small / error -> exit 0 silent, Claude reads the original

$ErrorActionPreference = 'SilentlyContinue'

# Kill switch — same ergonomics as the Bash/PowerShell Exec hooks.
$disabled = $env:THLIBO_DISABLED
if ($disabled -eq '1' -or $disabled -eq 'true' -or $disabled -eq 'on' -or $disabled -eq 'yes') {
    exit 0
}

$thlibo = Get-Command thlibo -ErrorAction SilentlyContinue
if (-not $thlibo) { exit 0 }

$raw = [Console]::In.ReadToEnd()
if (-not $raw) { exit 0 }

try { $obj = $raw | ConvertFrom-Json } catch { exit 0 }

$src = $obj.tool_input.file_path
if ([string]::IsNullOrEmpty($src)) { exit 0 }
if (-not (Test-Path -LiteralPath $src -PathType Leaf)) { exit 0 }

# Extension gate. Log-shaped extensions + binary formats Claude
# can't read natively (pdf). Other files pass through unmodified.
$ext = [System.IO.Path]::GetExtension($src).TrimStart('.').ToLowerInvariant()
$allowed = @('log','ndjson','txt','out','err','stderr','trace','dump','pdf')
if ($allowed -notcontains $ext) { exit 0 }

# Size gate. PDFs skip the gate — Claude can't usefully read PDF
# bytes at any size, so conversion is always worth running.
if ($ext -ne 'pdf') {
    $minBytes = 32768
    if ($env:THLIBO_READ_MIN_BYTES) {
        $parsed = 0
        if ([int]::TryParse($env:THLIBO_READ_MIN_BYTES, [ref]$parsed)) { $minBytes = $parsed }
    }
    $size = (Get-Item -LiteralPath $src).Length
    if ($size -lt $minBytes) { exit 0 }
}

# Don't double-compress. Check common path separators regardless of host.
$norm = $src -replace '\\','/'
if ($norm -like '*/.thlibo/cases/*') { exit 0 }

$caseDir = & thlibo case --quiet $src 2>$null
$caseExit = $LASTEXITCODE
if ($caseExit -ne 0 -or [string]::IsNullOrEmpty($caseDir)) { exit 0 }

$caseDir = $caseDir.TrimEnd("`r","`n")
$compressed = Join-Path $caseDir 'compressed.log'
if (-not (Test-Path -LiteralPath $compressed -PathType Leaf)) { exit 0 }

$obj.tool_input.file_path = $compressed

$out = [ordered]@{
    hookSpecificOutput = [ordered]@{
        hookEventName            = 'PreToolUse'
        permissionDecision       = 'allow'
        permissionDecisionReason = "thlibo auto-compressed: $src"
        updatedInput             = $obj.tool_input
    }
}
$out | ConvertTo-Json -Compress -Depth 10
