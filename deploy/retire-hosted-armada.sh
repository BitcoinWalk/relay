#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum --quiet -c CLEANUP-SHA256SUMS
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = bc4e1ad944ee99117908868d5380cd35a1f92492a81e0197a08ae51d834401cb || { echo 'Caddy changed; stop for review.' >&2; exit 1; }
if [ -e /srv/bitcoinwalk-chat-staging/join-site ]; then
 test -d /srv/bitcoinwalk-chat-staging/join-site && test ! -L /srv/bitcoinwalk-chat-staging/join-site
 test -f /srv/bitcoinwalk-chat-staging/join-site/index.html && test ! -L /srv/bitcoinwalk-chat-staging/join-site/index.html
 cmp -s login-test.html /srv/bitcoinwalk-chat-staging/join-site/index.html || { echo 'Retained join page differs; stop for review.' >&2; exit 1; }
fi
while IFS= read -r source; do
 test -e "$source" && test ! -L "$source" || { echo "Unexpected archive target: $source" >&2; exit 1; }
done < deploy/retire-armada-paths.txt
systemctl is-active --quiet bitcoinwalk-chat-staging
systemctl is-active --quiet bitcoinwalk-signer-staging
systemctl is-enabled --quiet bitcoinwalk-signer-staging
systemctl is-active --quiet caddy
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-armada-retired.XXXXXX)
chmod 0700 "$backup_dir"
cp -p /etc/caddy/Caddyfile "$backup_dir/Caddyfile"
cp deploy/retire-armada-paths.txt "$backup_dir/paths.txt"
caddy validate --config deploy/Caddyfile.external-combined --adapter caddyfile
rollback() {
 trap - EXIT
 set +e
 while IFS= read -r source; do
  archived="$backup_dir/retired$source"
  if [ -e "$archived" ]; then
   if [ -e "$source" ]; then echo "Restore conflict: $source; preserved archive." >&2
   else mv "$archived" "$source" || echo "Restore failed: $source" >&2; fi
  fi
 done < "$backup_dir/paths.txt"
 systemctl daemon-reload
 systemctl enable --now bitcoinwalk-signer-staging
 install -o root -g root -m 0644 "$backup_dir/Caddyfile" /etc/caddy/Caddyfile
 systemctl reload caddy
 echo "Cleanup failed at: $step; restoration attempted. Review services. Backup: $backup_dir" >&2
 echo 'New join-site directory retained for diagnosis. Relay data was not changed.' >&2
}
step='install standalone page and reload Caddy'
trap rollback EXIT
install -d -o root -g root -m 0755 /srv/bitcoinwalk-chat-staging/join-site
install -o root -g root -m 0644 login-test.html /srv/bitcoinwalk-chat-staging/join-site/index.html
install -o root -g root -m 0644 deploy/Caddyfile.external-combined /etc/caddy/Caddyfile
systemctl reload caddy
origin=https://chat-staging.bitcoinwalk.org
step='join page contents'
curl -fsS --max-time 10 --resolve chat-staging.bitcoinwalk.org:443:127.0.0.1 "$origin/join-chat" | cmp -s - login-test.html
echo 'PASS: standalone join page'
step='chat health and relay information'
curl -fsS --max-time 10 --resolve chat-staging.bitcoinwalk.org:443:127.0.0.1 "$origin/healthz"
curl -fsS --max-time 10 --resolve chat-staging.bitcoinwalk.org:443:127.0.0.1 -H 'Accept: application/nostr+json' "$origin/" >/dev/null
step='retired signer HTTP status'
status=$(curl -sS --max-time 10 --resolve chat-staging.bitcoinwalk.org:443:127.0.0.1 -o /dev/null -w '%{http_code}' "$origin/signer")
echo "Signer endpoint: HTTP $status (expected 410)"
test "$status" = 410
step='test page redirect'
status=$(curl -sS --max-time 10 --resolve chat-staging.bitcoinwalk.org:443:127.0.0.1 -o /dev/null -w '%{http_code}' "$origin/login-test.html")
echo "Test page redirect: HTTP $status (expected 302)"
test "$status" = 302
curl -fsS -L --max-redirs 2 --max-time 10 --resolve chat-staging.bitcoinwalk.org:443:127.0.0.1 "$origin/login-test.html" | cmp -s - login-test.html
step='organizer health'
curl -fsS --max-time 10 --resolve relay-staging.bitcoinwalk.org:443:127.0.0.1 https://relay-staging.bitcoinwalk.org/healthz
step='disable signer and archive obsolete assets'
systemctl disable --now bitcoinwalk-signer-staging
while IFS= read -r source; do
 archived="$backup_dir/retired$source"
 mkdir -p "$(dirname "$archived")"
 mv "$source" "$archived"
done < "$backup_dir/paths.txt"
systemctl daemon-reload
if systemctl is-active --quiet bitcoinwalk-signer-staging; then exit 1; fi
systemctl is-active --quiet bitcoinwalk-chat-staging
systemctl is-active --quiet caddy
trap - EXIT
echo
echo "Hosted Armada and signing service retired. Recoverable archive: $backup_dir"
echo 'Join page: https://chat-staging.bitcoinwalk.org/join-chat'
echo 'Relay binary, key, database, membership and legacy services unchanged.'
