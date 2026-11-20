#!/usr/bin/env bash
# Install or upgrade Pier on a remote Linux VPS in one command from this repo.
#
# Usage:
#   ./pier/scripts/vps-install.sh [--build] [--no-init] <user@host> [ssh options...]
#
# Examples:
#   ./pier/scripts/vps-install.sh root@203.0.113.10
#   ./pier/scripts/vps-install.sh ubuntu@203.0.113.10 -p 2222 -i ~/.ssh/id_ed25519
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
BUILD=0
RUN_INIT=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    --build) BUILD=1; shift ;;
    --no-init) RUN_INIT=0; shift ;;
    -h|--help)
      sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    --) shift; break ;;
    -*) echo "Unknown option: $1" >&2; exit 2 ;;
    *) break ;;
  esac
done

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 [--build] [--no-init] <user@host> [ssh options...]" >&2
  exit 2
fi

TARGET=$1
shift
SSH_OPTS=("$@")

# Reuse a single SSH connection so you only authenticate once.
CTL_DIR=$(mktemp -d "${TMPDIR:-/tmp}/pier-vps.XXXXXX")
CTL="$CTL_DIR/ssh.sock"
cleanup() {
  ssh -S "$CTL" -O exit "${SSH_OPTS[@]}" "$TARGET" >/dev/null 2>&1 || true
  rm -rf "$CTL_DIR"
}
trap cleanup EXIT

ssh_run() {
  ssh -o ControlMaster=auto -o ControlPath="$CTL" -o ControlPersist=5m "${SSH_OPTS[@]}" "$TARGET" "$@"
}

echo "==> Connecting to $TARGET and detecting platform..."
REMOTE_INFO=$(ssh_run 'printf "%s %s %s\n" "$(uname -s)" "$(uname -m)" "$(id -u)"')
read -r REMOTE_OS REMOTE_MACH REMOTE_UID <<<"$REMOTE_INFO"

if [[ "$REMOTE_OS" != "Linux" ]]; then
  echo "Pier server requires Linux (remote host reported $REMOTE_OS)." >&2
  exit 1
fi

case "$REMOTE_MACH" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Unsupported remote CPU architecture: $REMOTE_MACH" >&2; exit 1 ;;
esac

echo "    Remote: Linux $ARCH ($REMOTE_MACH)"

BUNDLE="$ROOT/pier/bin/releases/pier-linux-$ARCH.tar.gz"
if [[ "$BUILD" == 1 || ! -f "$BUNDLE" ]]; then
  echo "==> Building pier-linux-$ARCH.tar.gz locally..."
  (cd "$ROOT" && make pier-release ARCH="$ARCH")
else
  echo "==> Using existing bundle: $BUNDLE (pass --build to rebuild)"
fi

echo "==> Uploading bundle to $TARGET..."
REMOTE_TMP=$(ssh_run 'mktemp -d /tmp/pier-install.XXXXXX')
scp -o ControlPath="$CTL" "${SSH_OPTS[@]}" "$BUNDLE" "$TARGET:$REMOTE_TMP/pier-linux-$ARCH.tar.gz"

SUDO=""
if [[ "$REMOTE_UID" != "0" ]]; then
  SUDO="sudo"
fi

echo "==> Verifying and installing Pier on $TARGET..."
ssh_run -t "set -eu
  trap 'rm -rf \"$REMOTE_TMP\"' EXIT
  cd \"$REMOTE_TMP\"
  tar -xzf \"pier-linux-$ARCH.tar.gz\"
  cd \"pier-linux-$ARCH\"
  ./install.sh --check
  $SUDO ./install.sh
  if [ \"$RUN_INIT\" = 1 ] && $SUDO test ! -f /var/lib/pier/pier.db; then
    echo ''
    echo '==> Starting first-time setup (pierd init)...'
    $SUDO /usr/local/bin/pierd init
  fi"
