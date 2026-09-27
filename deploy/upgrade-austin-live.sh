#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c AUSTIN-LIVE-SHA256SUMS
test "$(sha256sum /opt/bitcoinwalk-austin-staging/bitcoinwalk-relay | cut -d ' ' -f 1)" = f0e1d2b0c78c8014415c45235ade922a8fd676dcf34ea8ff1d8d975a615737cb || { echo 'Unexpected installed version; stop for review.' >&2; exit 1; }
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-austin-live.XXXXXX)
chmod 0700 "$backup_dir"
cp -p /opt/bitcoinwalk-austin-staging/bitcoinwalk-relay "$backup_dir/bitcoinwalk-relay"
install -m 0600 /etc/bitcoinwalk-austin-staging/relay-key "$backup_dir/relay-key"
# Keep the stopped original DB in place; never restore a stale snapshot over it.
systemctl stop bitcoinwalk-austin-staging
failure() {
 systemctl stop bitcoinwalk-austin-staging || true
 echo "Upgrade stopped for review. Original binary/key and DB snapshot (if completed): $backup_dir" >&2
 echo 'Live database retained untouched by rollback. Do not downgrade after new metadata writes.' >&2
}
trap failure EXIT
RELAY_CHAT_DB=/var/lib/bitcoinwalk-austin-staging/chat.db RELAY_CHAT_KEY_FILE=/etc/bitcoinwalk-austin-staging/relay-key RELAY_CHAT_BACKUP="$backup_dir/chat.db" /opt/bitcoinwalk-austin-staging/bitcoinwalk-relay
install -o root -g root -m 0755 bitcoinwalk-austin-live /opt/bitcoinwalk-austin-staging/bitcoinwalk-relay
systemctl start bitcoinwalk-austin-staging
attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3337/healthz; do
 attempt=$((attempt+1)); test "$attempt" -lt 10 || exit 1; sleep 1
done
trap - EXIT
echo
echo "Austin multi-channel live update ready. Protected backup: $backup_dir"
echo 'Reconnect both Armada clients and test live messages in both directions. Profile lookup is unchanged.'
