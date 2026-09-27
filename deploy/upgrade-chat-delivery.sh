#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c DELIVERY-SHA256SUMS
test "$(sha256sum /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay | cut -d ' ' -f 1)" = b4e4e477bda9900197ef6f391fe4f16ab7bfd3efdc356fec259cab719b791984 || { echo 'Installed version changed; stop for review.' >&2; exit 1; }
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-chat-delivery.XXXXXX)
chmod 0700 "$backup_dir"
cp -p /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay "$backup_dir/bitcoinwalk-relay"
install -m 0600 /etc/bitcoinwalk-chat-staging/relay-key "$backup_dir/relay-key"
systemctl stop bitcoinwalk-chat-staging
failure() {
 systemctl stop bitcoinwalk-chat-staging || true
 echo "Upgrade stopped for review. Protected backup: $backup_dir" >&2
 echo 'Live database retained. No stale database restore performed.' >&2
}
trap failure EXIT
RELAY_CHAT_DB=/var/lib/bitcoinwalk-chat-staging/chat.db RELAY_CHAT_KEY_FILE=/etc/bitcoinwalk-chat-staging/relay-key RELAY_CHAT_BACKUP="$backup_dir/chat.db" /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
install -o root -g root -m 0755 bitcoinwalk-chat-delivery /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
systemctl start bitcoinwalk-chat-staging
attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3335/healthz; do
 attempt=$((attempt+1)); test "$attempt" -lt 10 || exit 1; sleep 1
done
trap - EXIT
echo
echo "Chat delivery update installed. Protected backup: $backup_dir"
echo 'Reconnect both Armada clients and test messages in each direction.'
echo 'Membership, privacy, signing service, Caddy and join page unchanged.'
