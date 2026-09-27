#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c AUSTIN-SHA256SUMS
for target in /opt/bitcoinwalk-austin-staging /etc/bitcoinwalk-austin-staging /var/lib/bitcoinwalk-austin-staging /var/lib/private/bitcoinwalk-austin-staging /etc/systemd/system/bitcoinwalk-austin-staging.service; do
 if [ -e "$target" ] || [ -L "$target" ]; then
  echo "Refusing existing target: $target. Stop for review; do not delete it." >&2
  exit 1
 fi
done
if ss -H -lnt | awk '{print $4}' | grep -Eq ':3337$'; then
 echo 'Port 3337 is occupied; nothing changed.' >&2; exit 1
fi
systemctl is-active --quiet bitcoinwalk-chat-staging
systemctl is-active --quiet bitcoinwalk-relay
systemctl is-active --quiet caddy
failure() {
 systemctl disable --now bitcoinwalk-austin-staging 2>/dev/null || true
 echo 'Austin setup failed; new service stopped, files retained for diagnosis. Existing services were not modified.' >&2
}
trap failure EXIT
install -d -o root -g root -m 0755 /opt/bitcoinwalk-austin-staging
install -d -o root -g root -m 0700 /etc/bitcoinwalk-austin-staging
install -o root -g root -m 0755 bitcoinwalk-austin-staging /opt/bitcoinwalk-austin-staging/bitcoinwalk-relay
install -o root -g root -m 0644 deploy/austin-staging.public.conf /etc/bitcoinwalk-austin-staging/public.conf
RELAY_CHAT_KEY_INIT=/etc/bitcoinwalk-austin-staging/relay-key /opt/bitcoinwalk-austin-staging/bitcoinwalk-relay
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-austin-key.XXXXXX)
chmod 0700 "$backup_dir"
install -m 0600 /etc/bitcoinwalk-austin-staging/relay-key "$backup_dir/relay-key"
install -o root -g root -m 0644 deploy/bitcoinwalk-austin-staging.service /etc/systemd/system/bitcoinwalk-austin-staging.service
systemctl daemon-reload
systemctl enable --now bitcoinwalk-austin-staging
attempt=0
until curl --fail --silent --max-time 2 http://127.0.0.1:3337/healthz; do
 attempt=$((attempt+1))
 test "$attempt" -lt 10 || exit 1
 sleep 1
done
systemctl is-active --quiet bitcoinwalk-chat-staging
systemctl is-active --quiet bitcoinwalk-relay
systemctl is-active --quiet caddy
trap - EXIT
echo
echo "Austin backend ready on loopback port 3337. Protected key backup: $backup_dir/relay-key"
echo 'Separate key and database; no group created yet. No Caddy, DNS, global chat or legacy changes.'
echo 'Next: verify staging DNS and configure HTTPS, then an administrator signs group creation.'
