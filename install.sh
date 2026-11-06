#!/bin/sh
# Laser Feet installer for macOS and Linux.
#
#   curl -fsSL https://billionairedev24.github.io/laserfeet/install.sh | sh
#
# It downloads lf for your system from the latest GitHub release, checks its
# SHA-256 checksum against the release's SHA256SUMS file, and only then installs it.
# Options (environment variables):
#   LF_VERSION=v0.1.0       install a specific version instead of the latest
#   LF_INSTALL_DIR=/path    where to put lf (default: ~/.local/bin)
set -eu

REPO="billionairedev24/laserfeet"
VERSION="${LF_VERSION:-latest}"
DIR="${LF_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
fail() { printf 'Laser Feet install failed: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=mac ;;
  *) fail "this installer supports macOS and Linux. On Windows, use install.ps1." ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "unsupported processor: $(uname -m)" ;;
esac
file="lf-$os-$arch"

if [ -n "${LF_BASE_URL:-}" ]; then
  base="$LF_BASE_URL"
elif [ "$VERSION" = "latest" ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

if command -v sha256sum >/dev/null 2>&1; then
  hash_of() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  hash_of() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "no SHA-256 tool found (need sha256sum or shasum), so the download can't be checked"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $file ($VERSION)..."
# Only HTTPS is allowed, except for a local test server set with LF_BASE_URL.
proto="=https"
[ -n "${LF_BASE_URL:-}" ] && proto="=http,https"
fetch() { curl -fsSL --proto "$proto" --tlsv1.2 -o "$1" "$2"; }
fetch "$tmp/$file" "$base/$file" || fail "could not download $base/$file"
fetch "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "could not download the checksum file"

expected="$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")"
[ -n "$expected" ] || fail "$file is not listed in SHA256SUMS"
actual="$(hash_of "$tmp/$file")"
if [ "$actual" != "$expected" ]; then
  fail "checksum mismatch for $file (expected $expected, got $actual). Nothing was installed."
fi
say "Checksum verified: $actual"

mkdir -p "$DIR"
chmod 755 "$tmp/$file"
mv "$tmp/$file" "$DIR/lf"
say "Installed lf to $DIR/lf"
"$DIR/lf" version

case ":$PATH:" in
  *":$DIR:"*) say "Try it:  lf new my-app" ;;
  *)
    say ""
    say "$DIR is not on your PATH yet. Add it with:"
    say "  echo 'export PATH=\"\$PATH:$DIR\"' >> ~/.zshrc && source ~/.zshrc"
    say "(use ~/.bashrc instead of ~/.zshrc if your shell is bash)"
    ;;
esac
