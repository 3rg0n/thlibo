# thlibo-hook-version: 1
# thlibo Claude Code PreToolUse hook for the Write + Edit tools — PowerShell.
#
# Pipes the envelope through `thlibo shorthand-hook`, which
# handles all decision logic (config gates, glob matching, eval
# checklist, content rewriting, original backup). Same shell-thin
# pattern as the Bash hook.

$ErrorActionPreference = 'SilentlyContinue'

# UTF-8 on every encoding this hook touches, and it matters most here: the
# envelope carries file CONTENT, and `thlibo shorthand-hook` writes what it
# gets back to disk. PowerShell 5.1 defaults [Console]::InputEncoding to
# the OEM page (IBM437 on a default box) and $OutputEncoding to ASCII, so
# without this an accented character was read as two OEM characters and
# then piped out as "??" -- measured: "cafe"+acute reached the child as
# `63 61 66 3f 3f`. Assigning [Console]::OutputEncoding calls
# SetConsoleOutputCP, which throws when no console is attached, so it
# degrades on its own rather than taking the hook down (#134).
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch { }

$disabled = $env:THLIBO_DISABLED
if ($disabled -eq '1' -or $disabled -eq 'true' -or $disabled -eq 'on' -or $disabled -eq 'yes') {
    exit 0
}

$thlibo = Get-Command thlibo -ErrorAction SilentlyContinue
if (-not $thlibo) { exit 0 }

# Explicit UTF-8 reader; [Console]::In would use the OEM page (#134).
$stdinReader = [System.IO.StreamReader]::new(
    [Console]::OpenStandardInput(), [System.Text.UTF8Encoding]::new($false))
$raw = $stdinReader.ReadToEnd()
if (-not $raw) { exit 0 }

# Pipe through, suppress stderr, always exit 0 — same fail-closed
# contract as every other thlibo hook.
$out = $raw | & thlibo shorthand-hook 2>$null
if ($out) { Write-Output $out }
exit 0
