#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root: sudo $0 <shockstock-linux-binary>" >&2
  exit 1
fi

if [[ $# -ne 1 ]] || [[ ! -f "$1" ]]; then
  echo "usage: $0 <shockstock-linux-binary>" >&2
  exit 1
fi

BINARY_SOURCE="$1"
INSTALL_PATH="/usr/local/bin/shockstock"
DATA_DIR="/var/lib/shockstock"
ENV_FILE="/etc/shockstock.env"
SERVICE_FILE="/etc/systemd/system/shockstock.service"
SERVICE_USER="shockstock"
SERVICE_GROUP="shockstock"

if ! getent group "${SERVICE_GROUP}" >/dev/null; then
  groupadd --system "${SERVICE_GROUP}"
fi

if ! id "${SERVICE_USER}" >/dev/null 2>&1; then
  useradd --system --home-dir "${DATA_DIR}" --no-create-home \
    --shell /usr/sbin/nologin --gid "${SERVICE_GROUP}" "${SERVICE_USER}"
fi

install -d -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" -m 0750 "${DATA_DIR}"
install -m 0755 "${BINARY_SOURCE}" "${INSTALL_PATH}.new"
mv "${INSTALL_PATH}.new" "${INSTALL_PATH}"

initial_password=""
if [[ ! -f "${ENV_FILE}" ]]; then
  initial_password="$(tr -dc 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789' < /dev/urandom | head -c 24)"
  umask 077
  cat > "${ENV_FILE}" <<EOF
LISTEN_ADDR=0.0.0.0:8080
AUTO_PORT=false
AUTO_PORT_ATTEMPTS=20
DATA_DIR=${DATA_DIR}
ADMIN_PASSWORD=${initial_password}
STOCK_SOURCE_URL=https://shockhosting.com/api/server-stock.php
AFFILIATE_ID=1469
EOF
  umask 022
fi

cat > "${SERVICE_FILE}" <<'EOF'
[Unit]
Description=Shock Hosting stock monitor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=shockstock
Group=shockstock
EnvironmentFile=/etc/shockstock.env
ExecStart=/usr/local/bin/shockstock
Restart=on-failure
RestartSec=5
TimeoutStopSec=15
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/shockstock
CapabilityBoundingSet=
LockPersonality=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
RestrictRealtime=true
SystemCallArchitectures=native
UMask=0027

[Install]
WantedBy=multi-user.target
EOF

chmod 0644 "${SERVICE_FILE}"
chmod 0600 "${ENV_FILE}"
systemctl daemon-reload
systemctl enable --now shockstock.service
systemctl restart shockstock.service

echo "Shock Stock is running on port 8080."
if [[ -n "${initial_password}" ]]; then
  echo "Initial admin password: ${initial_password}"
fi
