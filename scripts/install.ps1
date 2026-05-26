# thlibo one-line installer — Windows.
#
# Usage (PowerShell 5.1+ or PowerShell 7+):
#   irm https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.ps1 | iex
#
# Or pinned to a specific version:
#   $env:THLIBO_VERSION='v0.7.2'; irm https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.ps1 | iex
#
# Or against a local archive (CI / release verification):
#   $env:THLIBO_LOCAL_ARCHIVE='C:\path\to\thlibo-windows-amd64.zip'
#   .\scripts\install.ps1
# When set, the script skips the download + SHA-256 verify and uses
# that file directly. Intended for CI; users should not need this.
#
# What it does:
#   1. Downloads thlibo-windows-amd64.zip from the GitHub release.
#   2. Verifies SHA-256 against SHA256SUMS in the same release.
#   3. Extracts thlibo.exe into %LOCALAPPDATA%\thlibo\bin.
#   4. Adds that directory to the User PATH (via the Registry) so a
#      fresh shell finds the binary. No admin required.
#   5. Runs `thlibo install` to write Claude Code hooks, mirror
#      processors, and probe-or-install the inferd sidecar (which
#      handles the model download). Skip with $env:THLIBO_SKIP_INSTALL=1.
#
# What it does NOT do (on purpose):
#   - Touch the Machine PATH or any admin-only registry. This is a
#     per-user install (ADR 0004).
#
# Why no shim binary: we install one tool to one directory and add
# that directory to PATH. Chocolatey-style ShimGen shims solve a
# multi-package orchestration problem we don't have. See
# docs/adr/0004-no-windows-shim.md for the rationale.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Version   = if ($env:THLIBO_VERSION) { $env:THLIBO_VERSION } else { 'latest' }
$InstallDir = if ($env:THLIBO_INSTALL_DIR) { $env:THLIBO_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'thlibo\bin' }
$ReleasesApi = 'https://api.github.com/repos/3rg0n/thlibo/releases'

function Say($msg) { Write-Host "[thlibo install] $msg" }
function Die($msg, $code = 1) {
    Write-Error "[thlibo install] ERROR: $msg"
    exit $code
}

# TLS 1.2+ for older Windows PowerShell — .NET 4.8 defaults to
# SecurityProtocol=Ssl3|Tls, and GitHub requires 1.2 minimum.
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {}

# --- resolve the release tag ----------------------------------------

function Resolve-Tag {
    if ($script:Version -ne 'latest') { return $script:Version }
    # /latest is a public endpoint, no auth needed.
    $latest = Invoke-RestMethod -UseBasicParsing -Uri "$script:ReleasesApi/latest"
    if (-not $latest.tag_name) { Die 'could not resolve latest thlibo tag' 3 }
    return $latest.tag_name
}

# --- main -----------------------------------------------------------

try {
    $asset = 'thlibo-windows-amd64.zip'
    Say "install:  $InstallDir"

    $tmp = Join-Path $env:TEMP ("thlibo-install-" + [Guid]::NewGuid())
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
        $zipPath = Join-Path $tmp $asset

        if ($env:THLIBO_LOCAL_ARCHIVE) {
            # CI / release-verification path: use the supplied archive
            # directly, skip download + SHA verify.
            if (-not (Test-Path -LiteralPath $env:THLIBO_LOCAL_ARCHIVE)) {
                Die "THLIBO_LOCAL_ARCHIVE not found at $env:THLIBO_LOCAL_ARCHIVE" 3
            }
            Copy-Item -LiteralPath $env:THLIBO_LOCAL_ARCHIVE -Destination $zipPath
            $tag = if ($env:THLIBO_VERSION) { $env:THLIBO_VERSION } else { 'local' }
            Say "version:  $tag (from local archive $($env:THLIBO_LOCAL_ARCHIVE))"
        } else {
            $tag = Resolve-Tag
            $assetUrl = "https://github.com/3rg0n/thlibo/releases/download/$tag/$asset"
            $sumsUrl  = "https://github.com/3rg0n/thlibo/releases/download/$tag/SHA256SUMS"
            $sumsPath = Join-Path $tmp 'SHA256SUMS'

            Say "version:  $tag"
            Say "downloading $asset..."
            Invoke-WebRequest -UseBasicParsing -Uri $assetUrl -OutFile $zipPath
            Invoke-WebRequest -UseBasicParsing -Uri $sumsUrl  -OutFile $sumsPath

            Say 'verifying SHA-256...'
            $expectedLine = Get-Content $sumsPath | Where-Object { $_ -match "  ${asset}`$" }
            if (-not $expectedLine) { Die "SHA256SUMS does not reference $asset" 3 }
            $expected = ($expectedLine -split '\s+')[0].ToLower()
            $actual   = (Get-FileHash -Algorithm SHA256 -Path $zipPath).Hash.ToLower()
            if ($expected -ne $actual) { Die "SHA mismatch: expected=$expected actual=$actual" 3 }
        }

        # Extract into a temp folder, then move the exe to the final
        # location. Avoids partial state if something goes wrong
        # halfway through extraction.
        $extractRoot = Join-Path $tmp 'extract'
        Expand-Archive -Path $zipPath -DestinationPath $extractRoot -Force
        # Layout in the zip: thlibo-windows-amd64/bin/thlibo.exe
        $srcBin = Join-Path $extractRoot 'thlibo-windows-amd64\bin'
        if (-not (Test-Path $srcBin)) { Die "unexpected archive layout: $srcBin missing" 3 }

        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
        Copy-Item -Force (Join-Path $srcBin 'thlibo.exe')  (Join-Path $InstallDir 'thlibo.exe')

        Say "installed thlibo $tag → $InstallDir"
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }

    # --- User PATH registration ---
    # Pull the existing User PATH, append if missing, write back.
    # The Environment-variable target 'User' writes to HKCU and does
    # NOT require admin.
    $currentPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $currentPath) { $currentPath = '' }
    $entries = $currentPath.Split(';', [StringSplitOptions]::RemoveEmptyEntries)
    if ($entries -notcontains $InstallDir) {
        $newPath = if ($currentPath) { "$currentPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Say "added $InstallDir to User PATH."
        Say 'RESTART your shell (or log out and back in) for PATH changes to take effect.'
    } else {
        Say "$InstallDir already on User PATH."
    }

    # --- run `thlibo install` ---
    # The bare binary is on disk but not yet resolvable in this
    # session's PATH (Environment changes land in HKCU, not in
    # $env:Path for already-running processes). Call through the
    # absolute path.
    #
    # `thlibo install` writes Claude Code hooks, mirrors processors,
    # and probe-or-installs the inferd sidecar. Inferd handles the
    # ~5 GB model download on its first run; thlibo no longer pulls
    # the model itself.
    if ($env:THLIBO_SKIP_INSTALL -eq '1' -or
        $env:THLIBO_SKIP_INSTALL -eq 'true' -or
        $env:THLIBO_SKIP_INSTALL -eq 'yes') {
        Write-Host ''
        Say 'THLIBO_SKIP_INSTALL set — skipping configure step.'
        Say 'To finish manually later, run (from a fresh shell):'
        Say '    thlibo install'
    } else {
        Write-Host ''
        Say 'running: thlibo install'
        Say '  (writes Claude Code hooks, mirrors processors,'
        Say '   probe-or-installs the inferd sidecar; inferd then'
        Say '   downloads the ~5 GB Gemma 4 model on first request)'
        Write-Host ''
        $thliboExe = Join-Path $InstallDir 'thlibo.exe'
        if (-not (Test-Path $thliboExe)) {
            Die "thlibo.exe not found at $thliboExe after extraction" 4
        }
        # Forward exit code so automation / CI can key on install
        # success the same way it keys on the download step.
        & $thliboExe install
        $code = $LASTEXITCODE
        if ($code -ne 0) {
            Die "`"thlibo install`" exited $code" $code
        }

        Write-Host ''
        Say 'thlibo installed. Restart your shell so PATH picks up thlibo.exe,'
        Say 'then start a new Claude Code session — hooks will load automatically.'
    }
} catch {
    Die $_.Exception.Message 1
}
