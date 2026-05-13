# 0004. No Windows shim binary — plain PATH installation

- Status: accepted
- Date: 2026-05-13

## Context

When shipping a Windows binary via a one-line installer, there's a
choice between:

1. **Plain PATH installation.** Drop `thlibo.exe` into
   `%LOCALAPPDATA%\thlibo\bin` and add that directory to the User
   PATH (HKCU, no admin). This is what `gh`, `rg`, `hugo`, and most
   modern Go CLIs do.

2. **Shim-based installation.** Install the real binary to a
   versioned location (`%LOCALAPPDATA%\thlibo\<version>\bin`), then
   generate a small wrapper binary in a single canonical PATH dir
   that `CreateProcess`es the real binary. Chocolatey popularised
   this with ShimGen — documented at
   https://docs.chocolatey.org/en-us/features/shim/.

Both approaches have working reference implementations (see also
https://arsscriptum.github.io/blog/powershell-shim/ for the
ShimGen-driven variant).

## Decision

Plain PATH installation. No shim.

## Consequences

**Why this works for us:**

- thlibo ships two binaries — `thlibo.exe` and `thlibod.exe` — into
  one directory. A single PATH entry exposes both.
- There's no package-manager orchestration to worry about. No
  competing packages install into the same PATH dir, no
  versioned side-by-side concerns.
- Uninstall is `rmdir /s %LOCALAPPDATA%\thlibo` plus removing the
  PATH entry — both trivial without a shim index to consult.
- Argument forwarding, exit-code propagation, stdin/stdout/stderr
  piping, Ctrl+C handling are all handled by the Windows loader
  itself when PATH points at the real exe. No wrapper means no
  wrapper bugs.

**What we'd gain from a shim:**

- Per-version install directories that could be cleaned up
  individually. But v0.1 has no parallel versions; we upgrade
  in place.
- Protection against PATH-length-limit exhaustion from many
  packages each adding their own dir. Not our problem as a
  single-package installer.
- A hook to record usage, inject env vars, etc. We don't want any
  of that.

**Cost of the shim approach:**

- Either depend on an external tool (ShimGen from Chocolatey — adds
  a prerequisite we don't want for a curl-piped installer), or
  write our own `CreateProcessW`-based wrapper in C/Go/Rust and
  ship it as a third binary. Both options are overkill for the
  problem we actually have.

## References

- Chocolatey Shim docs:
  https://docs.chocolatey.org/en-us/features/shim/
- ShimGen-driven PowerShell module:
  https://arsscriptum.github.io/blog/powershell-shim/
- Installer implementation: `scripts/install.ps1`
