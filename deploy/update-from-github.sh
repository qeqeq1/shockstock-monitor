#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root: sudo $0 [tag]" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64)
    arch="amd64"
    ;;
  aarch64|arm64)
    arch="arm64"
    ;;
  *)
    echo "unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

tag="${1:-latest}"
if [[ "${tag}" == "latest" ]]; then
  url="https://github.com/qeqeq1/shockstock-monitor/releases/latest/download/shockstock-linux-${arch}"
else
  url="https://github.com/qeqeq1/shockstock-monitor/releases/download/${tag}/shockstock-linux-${arch}"
fi

tmp_file="$(mktemp /tmp/shockstock-linux-${arch}.XXXXXX)"
trap 'rm -f "${tmp_file}"' EXIT

echo "Downloading ${url}"
curl -fL --retry 3 --retry-delay 2 --connect-timeout 15 --max-time 300 \
  -o "${tmp_file}" "${url}"
chmod 0755 "${tmp_file}"
install -m 0755 "${tmp_file}" /usr/local/bin/shockstock

systemctl daemon-reload
systemctl restart shockstock.service
sleep 2
systemctl is-active shockstock.service
