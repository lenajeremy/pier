#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
arch=${1:-amd64}
case "$arch" in amd64|arm64) ;; *) echo 'Use amd64 or arm64.' >&2; exit 2;; esac
out=$PWD/bin/releases
mkdir -p "$out"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
name=pier-linux-$arch
stage=$work/$name
mkdir "$stage"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o "$stage/" ./cmd/...
cp install/install.sh install/pierd.service "$stage/"
printf '%s\n' "$arch" >"$stage/ARCH"
git describe --always --dirty >"$stage/VERSION"
(
 cd "$stage"
 if command -v sha256sum >/dev/null; then
  sha256sum pier pierd pierd.service ARCH VERSION
 else
  shasum -a 256 pier pierd pierd.service ARCH VERSION
 fi >SHA256SUMS
)
COPYFILE_DISABLE=1 tar -czf "$work/$name.tar.gz" -C "$work" "$name"
mv "$work/$name.tar.gz" "$out/$name.tar.gz"
printf 'Bundle: %s/%s.tar.gz\n' "$out" "$name"
