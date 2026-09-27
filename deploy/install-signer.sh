#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum --quiet -c SIGNER-SHA256SUMS
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = be3d8db39bfeebace3bb2f5759b9be4540bd3d46b228f2aab0d8993c661ad93f || { echo 'Caddy changed; stop for review.' >&2; exit 1; }
for target in /opt/bitcoinwalk-signer-staging /etc/systemd/system/bitcoinwalk-signer-staging.service /srv/bitcoinwalk-chat-staging/armada-before-signer /srv/bitcoinwalk-chat-staging/armada-signer-release /srv/bitcoinwalk-chat-staging/armada-signer-failed; do
 test ! -e "$target" || { echo "Target exists; stop for review: $target" >&2; exit 1; }
done
test -f /srv/bitcoinwalk-chat-staging/armada/index.html
curl -fsS --max-time 5 http://127.0.0.1:3335/healthz
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-signer.XXXXXX)
chmod 0700 "$backup_dir"
cp -p /etc/caddy/Caddyfile "$backup_dir/Caddyfile"
awk '{print} $0=="chat-staging.bitcoinwalk.org {" {while((getline line < "deploy/Caddyfile.signer-snippet")>0) print line; close("deploy/Caddyfile.signer-snippet")}' /etc/caddy/Caddyfile > "$backup_dir/Caddyfile.candidate"
caddy validate --config "$backup_dir/Caddyfile.candidate" --adapter caddyfile
changed_web=false
rollback() {
 install -o root -g root -m 0644 "$backup_dir/Caddyfile" /etc/caddy/Caddyfile
 systemctl reload caddy || echo 'WARNING: Caddy reload failed; operator review required.' >&2
 if [ "$changed_web" = true ]; then
  if [ -d /srv/bitcoinwalk-chat-staging/armada ]; then mv /srv/bitcoinwalk-chat-staging/armada /srv/bitcoinwalk-chat-staging/armada-signer-failed; fi
  mv /srv/bitcoinwalk-chat-staging/armada-before-signer /srv/bitcoinwalk-chat-staging/armada
 fi
 systemctl disable --now bitcoinwalk-signer-staging || true
 echo "Signing update failed; retained files for diagnosis. Caddy backup: $backup_dir/Caddyfile" >&2
}
trap rollback EXIT
install -d -m 0755 /opt/bitcoinwalk-signer-staging
install -o root -g root -m 0755 bitcoinwalk-signer /opt/bitcoinwalk-signer-staging/bitcoinwalk-signer
install -o root -g root -m 0644 deploy/bitcoinwalk-signer-staging.service /etc/systemd/system/bitcoinwalk-signer-staging.service
systemctl daemon-reload
systemctl enable --now bitcoinwalk-signer-staging
attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3336/healthz; do
 attempt=$((attempt+1)); test "$attempt" -lt 10 || exit 1; sleep 1
done
cp -R armada /srv/bitcoinwalk-chat-staging/armada-signer-release
chown -R root:root /srv/bitcoinwalk-chat-staging/armada-signer-release
find /srv/bitcoinwalk-chat-staging/armada-signer-release -type d -exec chmod 0755 {} +
find /srv/bitcoinwalk-chat-staging/armada-signer-release -type f -exec chmod 0644 {} +
mv /srv/bitcoinwalk-chat-staging/armada /srv/bitcoinwalk-chat-staging/armada-before-signer
changed_web=true
mv /srv/bitcoinwalk-chat-staging/armada-signer-release /srv/bitcoinwalk-chat-staging/armada
install -o root -g root -m 0644 "$backup_dir/Caddyfile.candidate" /etc/caddy/Caddyfile
systemctl reload caddy
curl -fsS --max-time 10 -H 'Accept: application/nostr+json' https://chat-staging.bitcoinwalk.org/signer
curl -fsS --max-time 10 https://chat-staging.bitcoinwalk.org/healthz
curl -fsS --max-time 10 https://relay-staging.bitcoinwalk.org/healthz
trap - EXIT
echo
echo "Signing endpoint installed. Caddy backup: $backup_dir/Caddyfile"
echo 'Previous web build retained at /srv/bitcoinwalk-chat-staging/armada-before-signer.'
echo 'Chat binary, key, database, membership rules and legacy services unchanged.'
