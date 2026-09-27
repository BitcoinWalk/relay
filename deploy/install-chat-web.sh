#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c WEB-SHA256SUMS
test -f armada/index.html
test ! -e /srv/bitcoinwalk-chat-staging || { echo 'Web target already exists; stop for review.' >&2; exit 1; }
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = 40a935c8458c035eea550ae0ee1a482cd7f9f5bfc998e774bf07ec37312d86a2 || { echo 'Caddy configuration changed; stop for review.' >&2; exit 1; }
systemctl is-active --quiet caddy
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:3335/healthz
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-chat-web.XXXXXX)
chmod 0700 "$backup_dir"
cp -p /etc/caddy/Caddyfile "$backup_dir/Caddyfile"
cp /etc/caddy/Caddyfile "$backup_dir/Caddyfile.candidate"
cat deploy/Caddyfile.chat-staging >> "$backup_dir/Caddyfile.candidate"
caddy validate --config "$backup_dir/Caddyfile.candidate" --adapter caddyfile
rollback() {
 install -o root -g root -m 0644 "$backup_dir/Caddyfile" /etc/caddy/Caddyfile
 if systemctl reload caddy; then
  echo 'Previous Caddy configuration restored. New static files retained for diagnosis.' >&2
 else
  echo "URGENT: Caddy reload failed; original config backup: $backup_dir/Caddyfile" >&2
 fi
}
trap rollback EXIT
install -d -o root -g root -m 0755 /srv/bitcoinwalk-chat-staging/armada
cp -R armada/. /srv/bitcoinwalk-chat-staging/armada/
chown -R root:root /srv/bitcoinwalk-chat-staging/armada
find /srv/bitcoinwalk-chat-staging/armada -type d -exec chmod 0755 {} +
find /srv/bitcoinwalk-chat-staging/armada -type f -exec chmod 0644 {} +
install -o root -g root -m 0644 "$backup_dir/Caddyfile.candidate" /etc/caddy/Caddyfile
systemctl reload caddy
# Check service health; public routing/TLS is verified separately after issuance.
systemctl is-active --quiet caddy
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:3334/healthz
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:3335/healthz
trap - EXIT
echo
echo "Staging web configuration installed. Caddy backup: $backup_dir/Caddyfile"
echo 'Next: independently verify public HTTPS/WSS and the Armada page before signing group creation.'
echo 'The join-chat entry remains disabled. No relay key/database or legacy hostname changed.'
