#!/bin/sh
# Install from the directory produced by make pier-release.
set -eu
cd "$(dirname "$0")"
case "${1:-}" in ""|--check) ;; *) echo "Usage: ./install.sh [--check]" >&2; exit 2;; esac
[ "$(uname -s)" = Linux ] || { echo 'Pier server requires Linux.' >&2; exit 1; }
case "$(uname -m)" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo 'Unsupported CPU architecture.' >&2; exit 1;; esac
[ "$(cat ARCH)" = "$arch" ] || { echo "Use the Linux $arch bundle." >&2; exit 1; }
# SHA256SUMS came in the same bundle as the files it lists. It catches a
# download or an unpacking that went wrong, and nothing else: whoever could
# change the bundle could change the sums with it.
sha256sum -c SHA256SUMS
printf 'Installing Pier %s for Linux %s\n' "$(cat VERSION)" "$arch"
df -h .
command -v nproc >/dev/null && printf 'CPUs: %s\n' "$(nproc)"
if command -v ss >/dev/null; then
 occupied=$(ss -H -ltn 'sport = :80 or sport = :443')
 [ -z "$occupied" ] || printf 'Ports 80 or 443 are already in use. Check before starting Pier.\n'
fi
if [ "${1:-}" = --check ]; then echo 'Bundle checks passed.'; exit 0; fi
[ "$(id -u)" = 0 ] || { echo 'Run the installer with sudo.' >&2; exit 1; }
command -v systemctl >/dev/null || { echo 'This installer requires systemd.' >&2; exit 1; }
command -v git >/dev/null || { echo 'Install git with your distribution package manager, then rerun.' >&2; exit 1; }
if ! command -v docker >/dev/null; then
 printf 'Docker is missing. Install Docker Engine and Buildx using apt? [y/N] '
 if [ -r /dev/tty ]; then
  read -r answer </dev/tty || answer=n
 else
  read -r answer || answer=n
 fi
 case "$answer" in y|Y|yes) command -v apt-get >/dev/null || { echo 'Install Docker Engine, then rerun.' >&2; exit 1; }; apt-get update
  if apt-cache show docker-buildx >/dev/null 2>&1; then
   apt-get install -y docker.io docker-buildx
  else
   echo 'Install Docker Engine and its Buildx plugin for your distribution, then rerun.' >&2
   exit 1
  fi
  systemctl enable --now docker;; *) echo 'Install Docker Engine, then rerun this installer.'; exit 1;; esac
fi
docker info >/dev/null || { echo 'Start Docker Engine, then rerun.' >&2; exit 1; }
docker buildx version >/dev/null || { echo 'Install the Docker Buildx CLI plugin for Railpack previews, then rerun.' >&2; exit 1; }
getent group docker >/dev/null || { echo 'Configure the docker group for access to the local Docker socket.' >&2; exit 1; }
getent group pier >/dev/null || groupadd --system pier
id pier >/dev/null 2>&1 || useradd --system --gid pier --home-dir /var/lib/pier --shell /usr/sbin/nologin pier
usermod -aG docker pier
install -d -m 0700 -o pier -g pier /var/lib/pier
install -d -m 0755 /usr/local/bin /etc/systemd/system
for binary in pier pierd; do
 tmp=$(mktemp /usr/local/bin/."$binary".XXXXXX)
 install -m 0755 "$binary" "$tmp"
 mv -f "$tmp" "/usr/local/bin/$binary"
done
install -m 0644 pierd.service /etc/systemd/system/pierd.service
systemctl daemon-reload
systemctl enable pierd
if systemctl is-active --quiet pierd; then
 systemctl restart pierd
 echo "Pier has been upgraded to $(/usr/local/bin/pierd version 2>/dev/null || echo 'the new release')."
else
 echo "Installed $(/usr/local/bin/pierd version 2>/dev/null || echo pierd)."
 echo 'Create the owner account and choose the server address:'
 echo '  sudo /usr/local/bin/pierd init'
fi
