#!/usr/bin/env bash
# thlibo one-line installer — Unix (Linux + macOS).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh | bash
#
# Or pinned to a specific version:
#   curl -fsSL https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh \
#     | THLIBO_VERSION=v0.7.2 bash
#
# Or against a local archive (CI / release verification):
#   THLIBO_LOCAL_ARCHIVE=/path/to/thlibo-linux-amd64.tar.gz bash scripts/install.sh
#   When set, the script skips the download + SHA-256 verify and uses
#   that file directly. Intended for CI; users should not need this.
#
# What it does:
#   1. Detects OS + architecture (linux/amd64, linux/arm64, darwin/arm64).
#   2. Downloads the matching tarball from the GitHub release.
#   3. Verifies SHA-256 against SHA256SUMS in the release.
#   4. Extracts `thlibo` into ~/.local/bin (creating it).
#   5. Runs `thlibo install` to wire Claude Code hooks, mirror
#      processors, and probe-or-install the inferd sidecar (which
#      handles the model download). Skip with THLIBO_SKIP_INSTALL=1.
#
# What it does NOT do (on purpose):
#   - Modify your shell rc. If ~/.local/bin isn't already on PATH it
#     prints the one-line addition you need to copy-paste.
#
# Exit codes:
#   0  success
#   1  unsupported OS/arch
#   2  prerequisite missing (curl or tar or sha256sum)
#   3  download / verification failure

set -euo pipefail

THLIBO_VERSION="${THLIBO_VERSION:-latest}"
INSTALL_DIR="${THLIBO_INSTALL_DIR:-$HOME/.local/bin}"
RELEASES_API="https://api.github.com/repos/3rg0n/thlibo/releases"

say() { printf '[thlibo install] %s\n' "$1"; }
die() { printf '[thlibo install] ERROR: %s\n' "$1" >&2; exit "${2:-1}"; }

# --- detect platform ------------------------------------------------

detect_platform() {
  local os arch
  case "$(uname -s)" in
    Linux)  os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS: $(uname -s). Supported: Linux, macOS." 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported arch: $(uname -m). Supported: amd64, arm64." 1 ;;
  esac
  # darwin-amd64 isn't in our release matrix (Apple Silicon only).
  if [ "$os" = "darwin" ] && [ "$arch" != "arm64" ]; then
    die "v0.1 macOS builds are Apple Silicon only. Build from source on Intel Macs." 1
  fi
  echo "${os}-${arch}"
}

require() {
  command -v "$1" >/dev/null 2>&1 || die "missing prerequisite: $1" 2
}

# --- resolve release URLs -------------------------------------------

resolve_tag() {
  if [ "$THLIBO_VERSION" != "latest" ]; then
    echo "$THLIBO_VERSION"
    return
  fi
  # Pass GITHUB_TOKEN if set — avoids 403 on rate-limited IPs.
  # grep+head keeps us from needing jq as a prerequisite.
  local auth_args=()
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    auth_args=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  fi
  # ${auth_args[@]+"${auth_args[@]}"} expands to nothing when the array
  # is empty — required on macOS bash 3.2 where set -u rejects
  # "${auth_args[@]}" on an empty array ("unbound variable").
  curl -fsSL ${auth_args[@]+"${auth_args[@]}"} "$RELEASES_API/latest" \
    | grep -oE '"tag_name":\s*"[^"]+"' \
    | head -n1 \
    | sed -E 's/.*"([^"]+)"$/\1/'
}

# --- main -----------------------------------------------------------

main() {
  require curl
  require tar
  require sha256sum

  local platform tag asset asset_url sums_url
  platform=$(detect_platform)

  say "platform: $platform"
  say "install:  $INSTALL_DIR"

  asset="thlibo-${platform}.tar.gz"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

  if [ -n "${THLIBO_LOCAL_ARCHIVE:-}" ]; then
    # CI / release-verification path: caller has already produced the
    # archive (e.g. release.yml's build matrix). Skip the download +
    # SHA verify; just use the file. Tag display is purely cosmetic
    # in this branch.
    [ -f "$THLIBO_LOCAL_ARCHIVE" ] || die "THLIBO_LOCAL_ARCHIVE not found at $THLIBO_LOCAL_ARCHIVE" 3
    cp "$THLIBO_LOCAL_ARCHIVE" "$tmp/$asset"
    tag="${THLIBO_VERSION:-local}"
    say "version:  $tag (from local archive $THLIBO_LOCAL_ARCHIVE)"
  else
    tag=$(resolve_tag)
    [ -n "$tag" ] || die "could not resolve thlibo release tag" 3
    asset_url="https://github.com/3rg0n/thlibo/releases/download/${tag}/${asset}"
    sums_url="https://github.com/3rg0n/thlibo/releases/download/${tag}/SHA256SUMS"

    say "version:  $tag"
    say "downloading $asset..."
    curl -fsSL "$asset_url" -o "$tmp/$asset"   || die "download failed: $asset_url" 3
    curl -fsSL "$sums_url"  -o "$tmp/SHA256SUMS" || die "download failed: $sums_url" 3

    # Verify the asset's SHA against SHA256SUMS. grep matches the
    # " <filename>" pattern sha256sum emits; standalone sha256sum
    # --check would require the file to be in cwd, so we do the
    # compare by hand.
    say "verifying SHA-256..."
    local expected actual
    expected=$(grep -E "[[:space:]]${asset}\$" "$tmp/SHA256SUMS" | awk '{print $1}')
    [ -n "$expected" ] || die "SHA256SUMS does not reference $asset" 3
    actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
    [ "$expected" = "$actual" ] || die "SHA mismatch: expected=$expected actual=$actual" 3
  fi

  mkdir -p "$INSTALL_DIR"
  say "extracting into $INSTALL_DIR..."
  tar -xzf "$tmp/$asset" -C "$tmp"
  # Tarball layout: thlibo-<plat>/bin/thlibo
  local extracted="$tmp/thlibo-${platform}"
  local dst="$INSTALL_DIR/thlibo"

  # Rename-then-replace, not overwrite-in-place. When `thlibo upgrade`
  # drives this script, $dst IS the running binary; on Linux a direct
  # write over a running executable fails with ETXTBSY ("Text file
  # busy") (#52). `install`/`mv` are rename-based so they're fine, but
  # we still move the old binary aside first, then install the new one
  # into the freed name — and roll the rename back if the install
  # fails, so an upgrade can never leave the user with no thlibo.
  if [ -e "$dst" ]; then
    local oldbin="$dst.old-$$"
    mv -f "$dst" "$oldbin" 2>/dev/null \
      || die "could not move the existing thlibo aside; close any running thlibo and retry" 5
    if ! install -m 755 "$extracted/bin/thlibo" "$dst"; then
      mv -f "$oldbin" "$dst" 2>/dev/null || true
      die "could not install the new thlibo; original restored, retry from a fresh shell" 5
    fi
  else
    install -m 755 "$extracted/bin/thlibo" "$dst"
  fi
  # Best-effort sweep of prior .old-* binaries from earlier upgrades.
  rm -f "$INSTALL_DIR"/thlibo.old-* 2>/dev/null || true

  # macOS Gatekeeper quarantines binaries downloaded from the internet.
  # Strip the flag so they run without a system "blocked" dialog.
  if [ "$(uname -s)" = "Darwin" ]; then
    xattr -d com.apple.quarantine "$INSTALL_DIR/thlibo"  2>/dev/null || true
  fi

  say "installed thlibo $tag → $INSTALL_DIR"

  # If $INSTALL_DIR isn't on PATH for the user's usual shell, print
  # the one-line addition they'll need. We still run thlibo below
  # via the absolute path, so the configure step works even when
  # the PATH line isn't there yet.
  if ! echo ":$PATH:" | grep -q ":${INSTALL_DIR}:"; then
    echo
    say "NOTE: $INSTALL_DIR is not on your PATH."
    say "  Add this to ~/.bashrc or ~/.zshrc for future shells:"
    say '    export PATH="$HOME/.local/bin:$PATH"'
  fi

  # --- run `thlibo install` ---------------------------------------
  #
  # `thlibo install` writes Claude Code hooks, mirrors processors,
  # and probe-or-installs the inferd sidecar. Inferd handles the
  # ~5 GB Gemma 4 model download on first inference request;
  # thlibo no longer ships the engine or pulls the model itself.
  case "${THLIBO_SKIP_INSTALL:-0}" in
    1|true|yes|on)
      echo
      say "THLIBO_SKIP_INSTALL set — skipping configure step."
      say "To finish manually later, run:"
      say "    $INSTALL_DIR/thlibo install"
      ;;
    *)
      echo
      say "running: $INSTALL_DIR/thlibo install"
      say "  (writes Claude Code hooks, mirrors processors,"
      say "   probe-or-installs the inferd sidecar; inferd then"
      say "   downloads the ~5 GB Gemma 4 model on first request)"
      echo
      # Absolute path: PATH in this shell may not yet include
      # $INSTALL_DIR even if a future rc source will.
      if ! "$INSTALL_DIR/thlibo" install; then
        die "\`thlibo install\` failed; re-run it manually from a fresh shell to retry" 4
      fi
      echo
      say "thlibo installed. Start a new Claude Code session —"
      say "hooks load automatically."
      ;;
  esac
}

main "$@"
