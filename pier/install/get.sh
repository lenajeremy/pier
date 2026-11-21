#!/bin/sh
# One-command Pier server installer for Linux (for curl -fsSL <url> | sudo sh).
#
# Environment overrides:
#   PIER_BASE_URL   Base URL hosting pier-linux-<arch>.tar.gz (overrides GitHub)
#   PIER_REPO       GitHub owner/repo hosting releases (e.g. lenajeremy/pier)
#   PIER_VERSION    Release tag (default: latest)
#   PIER_NO_INIT=1  Skip running `pierd init` after a fresh install
set -eu

# Everything runs from main, which is called on the last line. A download of
# this script that was cut short then runs nothing, instead of half of it as
# root.
main() {
  [ "$(uname -s)" = Linux ] || { echo 'Pier server requires Linux.' >&2; exit 1; }
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
  esac

  if [ "$(id -u)" != 0 ]; then
    echo 'Run this installer with sudo (for example: curl -fsSL <url> | sudo sh).' >&2
    exit 1
  fi

  archive="pier-linux-$arch.tar.gz"
  repo="${PIER_REPO:-lenajeremy/pier}"
  if [ -n "${PIER_BASE_URL:-}" ]; then
    url="${PIER_BASE_URL%/}/$archive"
  elif [ -n "${PIER_VERSION:-}" ] && [ "$PIER_VERSION" != latest ]; then
    url="https://github.com/$repo/releases/download/$PIER_VERSION/$archive"
  else
    url="https://github.com/$repo/releases/latest/download/$archive"
  fi

  work=$(mktemp -d /tmp/pier-get.XXXXXX)
  trap 'rm -rf "$work"' EXIT

  printf 'Downloading %s...\n' "$url"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$work/$archive"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$work/$archive" "$url"
  else
    echo 'Install curl or wget, then rerun.' >&2
    exit 1
  fi

  # install.sh --check compares the bundle with the SHA256SUMS inside it,
  # which catches a download or an unpacking that went wrong. It can't show
  # that the bundle is Pier's: that rests on HTTPS to the release host.
  tar -xzf "$work/$archive" -C "$work"
  cd "$work/pier-linux-$arch"
  ./install.sh --check
  ./install.sh

  if [ "${PIER_NO_INIT:-0}" != 1 ] && [ ! -f /var/lib/pier/pier.db ]; then
    if [ -r /dev/tty ]; then
      printf '\nStarting first-time setup (pierd init)...\n'
      /usr/local/bin/pierd init </dev/tty
    else
      echo 'Run setup interactively over SSH:  sudo /usr/local/bin/pierd init'
    fi
  fi
}

main "$@"
