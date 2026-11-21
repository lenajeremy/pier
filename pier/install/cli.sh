#!/bin/sh
# One-command Pier CLI installer for macOS and Linux.
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/lenajeremy/pier/main/pier/install/cli.sh | sh
#
# Environment overrides:
#   PIER_INSTALL_DIR  Destination directory (default: /opt/homebrew/bin, /usr/local/bin, or ~/.local/bin)
#   PIER_BASE_URL     Base URL hosting pier-cli-<os>-<arch>.tar.gz (overrides GitHub)
#   PIER_REPO         GitHub owner/repo hosting releases (default: lenajeremy/pier)
#   PIER_VERSION      Release tag (default: latest)
set -eu

# Everything runs from main, which is called on the last line. A download of
# this script that was cut short then runs nothing, instead of half of it.
main() {
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) echo "Unsupported operating system: $(uname -s)" >&2; exit 1 ;;
  esac

  case "$(uname -m)" in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64) arch=amd64 ;;
    *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
  esac

  archive="pier-cli-$os-$arch.tar.gz"
  repo="${PIER_REPO:-lenajeremy/pier}"
  if [ -n "${PIER_BASE_URL:-}" ]; then
    url="${PIER_BASE_URL%/}/$archive"
  elif [ -n "${PIER_VERSION:-}" ] && [ "$PIER_VERSION" != latest ]; then
    url="https://github.com/$repo/releases/download/$PIER_VERSION/$archive"
  else
    url="https://github.com/$repo/releases/latest/download/$archive"
  fi

  if [ -n "${PIER_INSTALL_DIR:-}" ]; then
    dest="$PIER_INSTALL_DIR"
  elif [ "$os" = darwin ] && [ -d /opt/homebrew/bin ] && [ -w /opt/homebrew/bin ]; then
    dest=/opt/homebrew/bin
  elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    dest=/usr/local/bin
  elif [ "$(id -u)" = 0 ]; then
    dest=/usr/local/bin
  else
    dest="${HOME}/.local/bin"
  fi

  work=$(mktemp -d "${TMPDIR:-/tmp}/pier-cli.XXXXXX")
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

  tar -xzf "$work/$archive" -C "$work"
  dir="$work/pier-cli-$os-$arch"
  cd "$dir"

  # SHA256SUMS comes inside the archive it checks. It catches a download or
  # an unpacking that went wrong, and nothing else: whoever could change the
  # archive could change the sums with it. What says the archive is Pier's
  # is HTTPS to the release host. Checking against a separately published,
  # signed checksum would need the release to publish one, and it doesn't.
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS >/dev/null
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -c SHA256SUMS >/dev/null
  else
    echo 'Neither sha256sum nor shasum found to verify download integrity.' >&2
    exit 1
  fi

  mkdir -p "$dest"
  tmp=$(mktemp "$dest/.pier.XXXXXX")
  install -m 0755 pier "$tmp"
  mv -f "$tmp" "$dest/pier"

  if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
    xattr -d com.apple.quarantine "$dest/pier" 2>/dev/null || true
  fi

  ver=$(cat VERSION)
  printf 'Installed pier (%s) to %s/pier\n' "$ver" "$dest"

  case ":$PATH:" in
    *":$dest:"*)
      printf '\nNext:\n  pier login <your-pier-domain>\n'
      ;;
    *)
      printf '\nAdd %s to your PATH so you can run pier from anywhere:\n' "$dest"
      printf '  export PATH="%s:$PATH"\n' "$dest"
      printf '\nThen sign in:\n  pier login <your-pier-domain>\n'
      ;;
  esac
}

main "$@"
