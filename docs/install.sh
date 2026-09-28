#!/bin/sh
# Re:Earth CLI installer.
#
#   curl -fsSL https://cli.reearth.io/install.sh | sh
#
# Environment variables:
#   REEARTH_VERSION      version to install (default: latest), e.g. v0.2.0
#   REEARTH_INSTALL_DIR  install directory (default: $HOME/.local/bin)
#
# The archive is verified against the release's SHA-256 checksums file.
set -eu

REPO="reearth/cli"
INSTALL_DIR="${REEARTH_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '  %s\n' "$*" >&2; }
fail() { printf '  \033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"; }
need curl
need tar
need uname

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS: $(uname -s) (on Windows, use winget or scoop)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "sha256sum or shasum is required to verify the download"
fi

tag="${REEARTH_VERSION:-}"
if [ -z "$tag" ]; then
  # /releases/latest redirects to /releases/tag/<tag>.
  tag="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/##')"
  [ -n "$tag" ] || fail "could not determine the latest version"
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac
version="${tag#v}"

archive="reearth_${version}_${os}_${arch}.tar.gz"
sums="reearth_${version}_checksums.txt"
base="https://github.com/$REPO/releases/download/$tag"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

say "Downloading reearth $version ($os/$arch)…"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "download failed: $base/$archive"
curl -fsSL -o "$tmp/$sums" "$base/$sums" || fail "download failed: $base/$sums"

want="$(grep " $archive\$" "$tmp/$sums" | cut -d' ' -f1)"
[ -n "$want" ] || fail "$archive is not listed in $sums"
got="$(sha256 "$tmp/$archive")"
[ "$want" = "$got" ] || fail "checksum mismatch for $archive (want $want, got $got)"

tar -xzf "$tmp/$archive" -C "$tmp" reearth
mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/reearth" "$INSTALL_DIR/reearth" 2>/dev/null || {
  cp "$tmp/reearth" "$INSTALL_DIR/reearth"
  chmod 0755 "$INSTALL_DIR/reearth"
}

printf '  \033[32m✓\033[0m Installed reearth %s to %s\n' "$version" "$INSTALL_DIR/reearth" >&2
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) say "Add $INSTALL_DIR to your PATH, e.g.:"
     say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.profile" ;;
esac
say "Get started with: reearth login"
