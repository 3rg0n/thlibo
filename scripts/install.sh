#!/usr/bin/env bash
# thlibo one-line installer — Unix (Linux + macOS).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh | bash
#
# Or pinned to a specific version:
#   curl -fsSL https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh \
#     | THLIBO_VERSION=v0.1.0 bash
#
# What it does:
#   1. Detects OS + architecture (linux/amd64, linux/arm64, darwin/arm64).
#   2. Downloads the matching tarball from the GitHub release.
#   3. Verifies SHA-256 against SHA256SUMS in the release.
#   4. Extracts `thlibo` and `thlibod` into ~/.local/bin (creating it).
#   5. Tells you what to do next.
#
# What it does NOT do (on purpose):
#   - Run `thlibo install`. That writes to ~/.claude/settings.json and
#     registers an autostart entry; you should run it manually after
#     reading what it does. This script's last line tells you how.
#   - Download the 5 GB Gemma model. Also deferred to `thlibo install`.
#   - Modify your shell rc. It prints the one-line PATH addition you
#     need to copy-paste.
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
  curl -fsSL "${auth_args[@]}" "$RELEASES_API/latest" \
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
  tag=$(resolve_tag)
  [ -n "$tag" ] || die "could not resolve thlibo release tag" 3

  say "platform: $platform"
  say "version:  $tag"
  say "install:  $INSTALL_DIR"

  asset="thlibo-${platform}.tar.gz"
  asset_url="https://github.com/3rg0n/thlibo/releases/download/${tag}/${asset}"
  sums_url="https://github.com/3rg0n/thlibo/releases/download/${tag}/SHA256SUMS"

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

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

  mkdir -p "$INSTALL_DIR"
  say "extracting into $INSTALL_DIR..."
  tar -xzf "$tmp/$asset" -C "$tmp"
  # Tarball layout: thlibo-<plat>/bin/{thlibo,thlibod}
  local extracted="$tmp/thlibo-${platform}"
  install -m 755 "$extracted/bin/thlibo"  "$INSTALL_DIR/thlibo"
  install -m 755 "$extracted/bin/thlibod" "$INSTALL_DIR/thlibod"

  # macOS Gatekeeper quarantines binaries downloaded from the internet.
  # Strip the flag so they run without a system "blocked" dialog.
  if [ "$(uname -s)" = "Darwin" ]; then
    xattr -d com.apple.quarantine "$INSTALL_DIR/thlibo"  2>/dev/null || true
    xattr -d com.apple.quarantine "$INSTALL_DIR/thlibod" 2>/dev/null || true
  fi

  say "installed thlibo $tag → $INSTALL_DIR"

  # --- next-steps guidance ---
  echo
  if ! echo ":$PATH:" | grep -q ":${INSTALL_DIR}:"; then
    say "NEXT STEP 1: add $INSTALL_DIR to your PATH."
    say "  e.g. add this to ~/.bashrc or ~/.zshrc:"
    say '    export PATH="$HOME/.local/bin:$PATH"'
    say "  then restart your shell or run \`source ~/.bashrc\`."
    echo
  fi
  say "NEXT STEP 2: finish the install:"
  say "    thlibo install --pull-engine --pull-model"
  say ""
  say "  --pull-engine  downloads the llamafile engine binary (~838 MB, required)."
  say "  --pull-model   downloads the Gemma 4 E4B GGUF model (~5 GB, required)."
  say "  This also wires up the Claude Code hook and registers the daemon"
  say "  for autostart. Run it once; both downloads are resumable."
}

main "$@"
