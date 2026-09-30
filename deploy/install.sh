#!/usr/bin/env bash
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "Run as root (sudo)." >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="${1:-$ROOT_DIR/webfile}"
if [[ ! -x "$BINARY" ]]; then
  echo "WebFile binary not found/executable: $BINARY" >&2
  exit 1
fi

if ! getent group webfile >/dev/null; then
  groupadd --system webfile
fi
if ! id webfile >/dev/null 2>&1; then
  useradd --system --gid webfile --home-dir /var/lib/webfile --shell /usr/sbin/nologin webfile
fi

install -Dm0755 "$BINARY" /usr/local/bin/webfile
install -d -m0755 -o root -g root /etc/webfile
install -d -m0750 -o root -g webfile /etc/webfile/tls
if [[ ! -f /etc/webfile/webfile.json ]]; then
  install -m0644 -o root -g root "$ROOT_DIR/webfile.example.json" /etc/webfile/webfile.json
  echo "Installed default config at /etc/webfile/webfile.json; review roots and cookie_secure before exposing WebFile."
fi
chown root:root /etc/webfile/webfile.json
chmod 0644 /etc/webfile/webfile.json
install -Dm0644 "$ROOT_DIR/deploy/webfile.service" /etc/systemd/system/webfile.service
install -Dm0644 "$ROOT_DIR/deploy/webfile-launcher.service" /etc/systemd/system/webfile-launcher.service
install -Dm0644 "$ROOT_DIR/deploy/webfile.tmpfiles" /usr/lib/tmpfiles.d/webfile.conf
install -Dm0644 "$ROOT_DIR/deploy/webfile.pam" /etc/pam.d/webfile
install -Dm0755 "$ROOT_DIR/deploy/webfile-update" /usr/local/sbin/webfile-update

systemd-tmpfiles --create /usr/lib/tmpfiles.d/webfile.conf
systemctl daemon-reload
if [[ ! -r /etc/webfile/tls/webfile.crt || ! -r /etc/webfile/tls/webfile.key ]]; then
  echo "Install a TLS certificate and private key before starting WebFile:" >&2
  echo "  /etc/webfile/tls/webfile.crt" >&2
  echo "  /etc/webfile/tls/webfile.key" >&2
  echo "The private key should be owned by root:webfile with mode 0640." >&2
  exit 1
fi
systemctl enable --now webfile-launcher.service webfile.service

echo "WebFile installed."
echo "  Config: /etc/webfile/webfile.json"
echo "  Status: systemctl status webfile webfile-launcher"
