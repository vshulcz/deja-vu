#!/bin/sh
set -eu

repo="vshulcz/deja-vu"
bin="deja"

fail() {
  printf '%s\n' "deja install: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

need curl

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) fail "unsupported OS: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) fail "unsupported arch: $arch" ;;
esac

api="https://api.github.com/repos/$repo/releases/latest"
tag=$(curl -fsSL "$api" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | sed -n '1p')
[ -n "$tag" ] || fail "could not detect latest release tag"
version=${tag#v}

archive="deja-vu_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"
tmp=${TMPDIR:-/tmp}/deja-vu-install.$$

cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM
mkdir -p "$tmp"

curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

sum=$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")
[ -n "$sum" ] || fail "checksum entry not found for $archive"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
  fail "missing sha256sum or shasum"
fi
[ "$actual" = "$sum" ] || fail "checksum mismatch"

tar -xzf "$tmp/$archive" -C "$tmp"
[ -x "$tmp/$bin" ] || fail "archive did not contain executable $bin"

dest_dir=${DEJA_INSTALL_DIR:-$HOME/.local/bin}
if [ ! -d "$dest_dir" ]; then
  if mkdir -p "$dest_dir" 2>/dev/null; then
    :
  else
    dest_dir=/usr/local/bin
  fi
fi

if [ -w "$dest_dir" ]; then
  install -m 0755 "$tmp/$bin" "$dest_dir/$bin"
else
  need sudo
  sudo install -m 0755 "$tmp/$bin" "$dest_dir/$bin"
fi

printf 'installed %s %s to %s/%s\n' "$bin" "$tag" "$dest_dir" "$bin"

# shellcheck disable=SC2016  # $PATH is intentionally literal in the hint
case ":$PATH:" in
  *":$dest_dir:"*) ;;
  *) [ "$dest_dir" = "$HOME/.local/bin" ] && printf 'hint: add export PATH=%s:$PATH to your shell profile\n' "$HOME/.local/bin" ;;
esac
