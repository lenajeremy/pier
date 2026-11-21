#!/bin/sh
# Package standalone pier CLI tarballs for macOS and Linux.
# Usage:
#   pier/scripts/release-cli.sh              # all supported targets
#   pier/scripts/release-cli.sh darwin arm64 # a single target
set -eu
cd "$(dirname "$0")/.."

out=$PWD/bin/releases
mkdir -p "$out"
ver=${VERSION:-$(git describe --tags --always --dirty)}

build_one() {
  os=$1
  arch=$2
  case "$os" in darwin|linux) ;; *) echo "Unsupported OS: $os" >&2; exit 2 ;; esac
  case "$arch" in amd64|arm64) ;; *) echo "Unsupported arch: $arch" >&2; exit 2 ;; esac

  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  name="pier-cli-$os-$arch"
  stage="$work/$name"
  mkdir "$stage"

  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-X pier/internal/version.Version=$ver" -o "$stage/pier" ./cmd/pier
  printf '%s\n' "$os" >"$stage/OS"
  printf '%s\n' "$arch" >"$stage/ARCH"
  printf '%s\n' "$ver" >"$stage/VERSION"
  (
    cd "$stage"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum pier OS ARCH VERSION
    else
      shasum -a 256 pier OS ARCH VERSION
    fi >SHA256SUMS
  )
  COPYFILE_DISABLE=1 tar -czf "$work/$name.tar.gz" -C "$work" "$name"
  mv "$work/$name.tar.gz" "$out/$name.tar.gz"
  rm -rf "$work"
  trap - EXIT
  printf 'Bundle: %s/%s.tar.gz\n' "$out" "$name"
}

if [ "$#" -eq 0 ]; then
  build_one darwin arm64
  build_one darwin amd64
  build_one linux amd64
  build_one linux arm64
elif [ "$#" -eq 2 ]; then
  build_one "$1" "$2"
else
  echo "Usage: $0 [darwin|linux amd64|arm64]" >&2
  exit 2
fi
