#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c CHAT-STAGING-SHA256SUMS
for target in /opt/bitcoinwalk-chat-staging /etc/bitcoinwalk-chat-staging /var/lib/bitcoinwalk-chat-staging /etc/systemd/system/bitcoinwalk-chat-staging.service; do
 if [ -e "$target" ] || [ -L "$target" ]; then
  echo "Refusing to overwrite existing target: $target" >&2
  exit 1
 fi
done
if ss -H -lnt | awk '{print $4}' | grep -Eq ':3335$'; then
 echo 'Port 3335 is occupied; nothing changed.' >&2; exit 1
fi
install -d -o root -g root -m 0755 /opt/bitcoinwalk-chat-staging
install -d -o root -g root -m 0700 /etc/bitcoinwalk-chat-staging
install -o root -g root -m 0755 bitcoinwalk-chat-staging /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
install -o root -g root -m 0644 deploy/chat-staging.public.conf /etc/bitcoinwalk-chat-staging/public.conf
RELAY_CHAT_KEY_INIT=/etc/bitcoinwalk-chat-staging/relay-key /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-chat-key.XXXXXX)
chmod 0700 "$backup_dir"
install -m 0600 /etc/bitcoinwalk-chat-staging/relay-key "$backup_dir/relay-key"
install -o root -g root -m 0644 deploy/bitcoinwalk-chat-staging.service /etc/systemd/system/bitcoinwalk-chat-staging.service
systemctl daemon-reload
if ! systemctl enable --now bitcoinwalk-chat-staging; then
 systemctl disable --now bitcoinwalk-chat-staging || true
 echo 'Chat staging start failed. New files retained for diagnosis; no proxy changes made.' >&2
 exit 1
fi
attempt=0
until curl --fail --silent http://127.0.0.1:3335/healthz; do
 attempt=$((attempt+1))
 if [ "$attempt" -ge 10 ]; then
  systemctl disable --now bitcoinwalk-chat-staging || true
  echo 'Health check failed. New service stopped; files retained for diagnosis.' >&2
  exit 1
 fi
 sleep 1
done
echo
echo 'Persistent chat backend installed on loopback port 3335 only.'
echo "Protected initial relay-key backup: $backup_dir/relay-key"
echo 'No Caddy, DNS, firewall, legacy service or organizer changes were made.'
echo 'Do not publish this backend yet. HTTPS/client setup and admin group creation are separate steps.'
