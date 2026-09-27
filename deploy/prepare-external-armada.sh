#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c EXTERNAL-SHA256SUMS
test "$(sha256sum /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay | cut -d ' ' -f 1)" = c13b26b95360ee2650015b3c03b9f21d360403a34977d123f051853b0ecc47a5 || { echo 'Installed relay changed; stop for review.' >&2; exit 1; }
page=/srv/bitcoinwalk-chat-staging/armada/login-test.html
test -f "$page" && test ! -L "$page"
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-external-armada.XXXXXX)
chmod 0700 "$backup_dir"
cp -p /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay "$backup_dir/bitcoinwalk-relay"
cp -p "$page" "$backup_dir/login-test.html"
install -m 0600 /etc/bitcoinwalk-chat-staging/relay-key "$backup_dir/relay-key"
systemctl stop bitcoinwalk-chat-staging
failure() {
 systemctl stop bitcoinwalk-chat-staging || true
 echo "Preparation failed; service stopped for review. Protected backups: $backup_dir" >&2
 echo 'Live database retained; no automatic stale-database restore.' >&2
}
trap failure EXIT
RELAY_CHAT_DB=/var/lib/bitcoinwalk-chat-staging/chat.db RELAY_CHAT_KEY_FILE=/etc/bitcoinwalk-chat-staging/relay-key RELAY_CHAT_BACKUP="$backup_dir/chat.db" /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
install -o root -g root -m 0755 bitcoinwalk-chat-external /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
systemctl start bitcoinwalk-chat-staging
attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3335/healthz; do
 attempt=$((attempt+1)); test "$attempt" -lt 10 || exit 1; sleep 1
done
install -o root -g root -m 0644 login-test.html "$page"
trap - EXIT
echo
echo "External Armada preparation installed. Protected backup: $backup_dir"
echo 'Open https://chat-staging.bitcoinwalk.org/login-test.html and refresh.'
echo 'Hosted Armada and signer service retained temporarily until external login/join/send tests pass.'
