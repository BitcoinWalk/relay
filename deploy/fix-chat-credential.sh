#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
test "$(sha256sum bitcoinwalk-chat-credential-fix | cut -d ' ' -f 1)" = d9b80f0308129cbb62834e5c048e4da6d14b9c06fb16d85d59fa8a1454afd705 || { echo 'New binary checksum mismatch.' >&2; exit 1; }
test "$(sha256sum /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay | cut -d ' ' -f 1)" = 95fae26f9ff7cec5f40d63ea3a2dcf679178edae5e7ea7c87a9a1a19b13c09b3 || { echo 'Installed binary differs from expected failed release; stop for review.' >&2; exit 1; }
if systemctl is-active --quiet bitcoinwalk-chat-staging; then
 echo 'Service is already active; stop for review without replacing it.' >&2; exit 1
fi
test -f /etc/bitcoinwalk-chat-staging/relay-key
test -f /etc/systemd/system/bitcoinwalk-chat-staging.service
systemctl daemon-reload
test "$(systemctl show bitcoinwalk-chat-staging.service -p LoadState --value)" = loaded || { echo 'Staging unit could not be loaded; no binary changed.' >&2; exit 1; }
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-chat-binary.XXXXXX)
chmod 0700 "$backup_dir"
cp -p /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay "$backup_dir/bitcoinwalk-relay"
rollback() {
 systemctl disable --now bitcoinwalk-chat-staging || true
 install -o root -g root -m 0755 "$backup_dir/bitcoinwalk-relay" /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
 echo 'Update failed: old binary restored, service stopped. Key and database retained.' >&2
}
trap rollback EXIT
install -o root -g root -m 0755 bitcoinwalk-chat-credential-fix /opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
if systemctl is-failed --quiet bitcoinwalk-chat-staging.service; then
 systemctl reset-failed bitcoinwalk-chat-staging.service
fi
systemctl enable --now bitcoinwalk-chat-staging
attempt=0
until curl --fail --silent --show-error --max-time 2 http://127.0.0.1:3335/healthz; do
 attempt=$((attempt+1))
 if [ "$attempt" -ge 10 ]; then
  echo 'Health check failed; please share sudo journalctl -u bitcoinwalk-chat-staging --no-pager -n 30' >&2
  exit 1
 fi
 sleep 1
done
trap - EXIT
echo
echo "Credential compatibility update complete. Old binary backup: $backup_dir"
echo 'Relay key, database, service configuration, Caddy and legacy services were not changed.'
