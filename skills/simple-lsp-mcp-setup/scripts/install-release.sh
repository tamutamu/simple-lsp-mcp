#!/bin/sh
# Install the official checked release artifact into the current user's bin dir.
# No sudo, no shell-profile modifications, and no implicit execution of the binary.
set -eu

for tool in curl tar awk mktemp uname; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing prerequisite: $tool" >&2; exit 1; }
done

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo 'unsupported OS: use the Windows installer on Windows' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo 'unsupported architecture' >&2; exit 1 ;;
esac

repo='https://github.com/tamutamu/simple-lsp-mcp'
tag_url=$(curl --fail --silent --show-error --location --output /dev/null --write-out '%{url_effective}' "$repo/releases/latest")
case "$tag_url" in
  "$repo/releases/tag/"v[0-9]*) tag=${tag_url##*/} ;;
  *) echo "could not resolve an official release tag: $tag_url" >&2; exit 1 ;;
esac
version=${tag#v}
archive="simple-lsp-mcp_${version}_${os}_${arch}.tar.gz"
base="$repo/releases/download/$tag"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
curl --fail --silent --show-error --location "$base/$archive" --output "$tmp/$archive"
curl --fail --silent --show-error --location "$base/checksums.txt" --output "$tmp/checksums.txt"
expected=$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")
[ -n "$expected" ] || { echo "checksum not published for $archive" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
  echo 'missing SHA-256 checksum utility' >&2; exit 1
fi
[ "$actual" = "$expected" ] || { echo "checksum mismatch for $archive" >&2; exit 1; }

tar -xzf "$tmp/$archive" -C "$tmp" simple-lsp-mcp
if [ -n "${SIMPLE_LSP_INSTALL_DIR:-}" ]; then
  dest=$SIMPLE_LSP_INSTALL_DIR
else
  dest="$HOME/.local/bin"
fi
mkdir -p "$dest"
# Do not overwrite a target symlink (a user-managed binary or external path).
[ ! -L "$dest/simple-lsp-mcp" ] || { echo 'refusing to replace symlink' >&2; exit 1; }
install -m 755 "$tmp/simple-lsp-mcp" "$dest/simple-lsp-mcp"
printf '%s\n' "$dest/simple-lsp-mcp"
