#!/bin/sh
# standup installer — https://github.com/jhonsanchez/standup
#   curl -fsSL https://raw.githubusercontent.com/jhonsanchez/standup/main/install.sh | sh
# Env: STANDUP_INSTALL_DIR (default /usr/local/bin)
set -eu

REPO="jhonsanchez/standup"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux | darwin) ;;
  *)
    echo "unsupported OS: $os — download manually from https://github.com/$REPO/releases"
    exit 1
    ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *)
    echo "unsupported architecture: $arch"
    exit 1
    ;;
esac

# Resolve the latest tag from the release redirect (no API, no rate limits).
tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/##')
[ -n "$tag" ] || {
  echo "could not resolve the latest release"
  exit 1
}
ver=${tag#v}

url="https://github.com/$REPO/releases/download/$tag/standup_${ver}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading standup $tag ($os/$arch)…"
curl -fsSL "$url" | tar xz -C "$tmp" standup

# Default to a user-owned directory so future `standup upgrade` runs never
# need sudo (set STANDUP_INSTALL_DIR=/usr/local/bin for a system-wide install).
dir="${STANDUP_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dir" 2>/dev/null || true
if [ -d "$dir" ] && [ -w "$dir" ]; then
  install "$tmp/standup" "$dir/standup"
else
  echo "installing to $dir (needs sudo)…"
  sudo install "$tmp/standup" "$dir/standup"
fi

echo "✓ installed: $("$dir/standup" --version)"
case ":$PATH:" in
  *:"$dir":*) ;;
  *)
    echo "  ⚠ $dir is not on your PATH — add this to your shell profile:"
    echo "      export PATH=\"$dir:\$PATH\""
    ;;
esac
echo "  run 'standup' to get started — config is created on first run"
