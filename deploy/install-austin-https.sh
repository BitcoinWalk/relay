#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c AUSTIN-HTTPS-SHA256SUMS
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = 5325def774989d17347c80eee4f7c2ba44aa1b7e04a12f9e63631210844acd2a || { echo 'Caddy configuration changed; stop for review.' >&2; exit 1; }
for unit in bitcoinwalk-austin-staging bitcoinwalk-chat-staging bitcoinwalk-relay caddy; do
 systemctl is-active --quiet "$unit"
done
for port in 3334 3335 3337; do
 curl --fail --silent --show-error --max-time 3 "http://127.0.0.1:$port/healthz"
done
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-austin-https.XXXXXX)
chmod 0700 "$backup_dir"
cp -p /etc/caddy/Caddyfile "$backup_dir/Caddyfile"
cat /etc/caddy/Caddyfile deploy/Caddyfile.austin-staging > "$backup_dir/Caddyfile.candidate"
caddy validate --config "$backup_dir/Caddyfile.candidate" --adapter caddyfile
rollback() {
 install -o root -g root -m 0644 "$backup_dir/Caddyfile" /etc/caddy/Caddyfile
 if systemctl reload caddy; then
  echo "Previous Caddy configuration restored. Backup: $backup_dir" >&2
 else
  echo "URGENT: restoration reload failed. Review Caddy; backup: $backup_dir" >&2
 fi
}
trap rollback EXIT
install -o root -g root -m 0644 "$backup_dir/Caddyfile.candidate" /etc/caddy/Caddyfile
systemctl reload caddy
for unit in bitcoinwalk-austin-staging bitcoinwalk-chat-staging bitcoinwalk-relay caddy; do
 systemctl is-active --quiet "$unit"
done
for port in 3334 3335 3337; do
 curl --fail --silent --show-error --max-time 3 "http://127.0.0.1:$port/healthz"
done
trap - EXIT
echo
echo "Austin HTTPS configuration installed. Protected Caddy backup: $backup_dir/Caddyfile"
echo 'Certificate issuance and public HTTPS/WSS must be verified next, before group creation.'
echo 'Join link remains disabled. Relay keys, databases, global and legacy host blocks unchanged.'
