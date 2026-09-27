#!/bin/sh
set -eu
if [ "$(id -u)" -ne 0 ]; then
 echo 'Run with sudo.' >&2
 exit 1
fi
cd "$(dirname "$0")"
test -f Caddyfile.staging
# This installer is for the verified fresh VPS, not an existing proxy host.
if command -v caddy >/dev/null 2>&1 || [ -e /etc/caddy/Caddyfile ]; then
 echo 'Caddy already exists. Stop here so its configuration can be reviewed; nothing changed.' >&2
 exit 1
fi
if ss -H -lnt | awk '{print $4}' | grep -Eq ':(80|443)$'; then
 echo 'Port 80 or 443 is occupied. Stop for review; nothing changed.' >&2
 exit 1
fi
curl --fail --silent --max-time 5 http://127.0.0.1:3334/healthz
apt-get update
apt-get install -y caddy
caddy validate --config "$PWD/Caddyfile.staging" --adapter caddyfile
backup_dir=$(mktemp -d /etc/caddy/bitcoinwalk-backup.XXXXXX)
cp -p /etc/caddy/Caddyfile "$backup_dir/Caddyfile"
echo "Original Caddy configuration saved to $backup_dir/Caddyfile"
install -o root -g root -m 0644 Caddyfile.staging /etc/caddy/Caddyfile
systemctl enable --now caddy
if ! systemctl reload caddy; then
 cp -p "$backup_dir/Caddyfile" /etc/caddy/Caddyfile
 systemctl reload caddy || true
 echo 'Reload failed; original configuration restored.' >&2
 exit 1
fi
systemctl --no-pager status caddy
echo 'Staging HTTPS configuration activated. Certificate issuance may wait for DNS caches.'
echo 'No DNS, SSH or firewall rules were changed. Public TCP ports 80 and 443 must be reachable.'
