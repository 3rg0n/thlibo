# thlibo-hook-version: 1
# thlibo Claude Code PreToolUse hook for the Write + Edit tools — PowerShell.
#
# Pipes the envelope through `thlibo shorthand-hook`, which
# handles all decision logic (config gates, glob matching, eval
# checklist, content rewriting, original backup). Same shell-thin
# pattern as the Bash hook.

$ErrorActionPreference = 'SilentlyContinue'

$disabled = $env:THLIBO_DISABLED
if ($disabled -eq '1' -or $disabled -eq 'true' -or $disabled -eq 'on' -or $disabled -eq 'yes') {
    exit 0
}

$thlibo = Get-Command thlibo -ErrorAction SilentlyContinue
if (-not $thlibo) { exit 0 }

$raw = [Console]::In.ReadToEnd()
if (-not $raw) { exit 0 }

# Pipe through, suppress stderr, always exit 0 — same fail-closed
# contract as every other thlibo hook.
$out = $raw | & thlibo shorthand-hook 2>$null
if ($out) { Write-Output $out }
exit 0
