#!/bin/sh
set -eu

test "$(id -u)" -eq 0 || { echo "Run with sudo." >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c CHAT-LONDON-0.8.96-SHA256SUMS

binary=bitcoinwalk-chat-london-0.8.96
unit=bitcoinwalk-chat-london-0.8.96.service
caddy_snippet=chat-london-0.8.96.caddy
target_unit=/etc/systemd/system/bitcoinwalk-chat-london.service

test -x "$binary"
test -f "deploy/$unit"
test -f "deploy/$caddy_snippet"
test -f /etc/caddy/Caddyfile
systemctl is-active --quiet bitcoinwalk-chat.service
curl -fsS --max-time 5 http://127.0.0.1:3339/healthz >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:3338/api/healthz >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:3345/api/healthz >/dev/null

for target in /opt/bitcoinwalk-chat-london /etc/bitcoinwalk-chat-london /var/lib/bitcoinwalk-chat-london "$target_unit"; do
 test ! -e "$target" || { echo "Existing London community target requires operator review: $target" >&2; exit 1; }
done
! grep -q 'chat-london\.bitcoinwalk\.org' /etc/caddy/Caddyfile || { echo "Caddy already contains chat-london.bitcoinwalk.org" >&2; exit 1; }
! ss -ltnH '( sport = :3346 )' | grep -q . || { echo "Loopback port 3346 is already in use" >&2; exit 1; }

backup=$(mktemp -d /var/backups/bitcoinwalk-chat-london.XXXXXX)
chmod 0700 "$backup"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile.before"
printf '%s\n' "$(sha256sum /etc/caddy/Caddyfile)" >"$backup/Caddyfile.before.sha256"

cleanup() {
 status=$?
 if test "$status" -ne 0; then
  systemctl disable --now bitcoinwalk-chat-london.service >/dev/null 2>&1 || true
  cp -p "$backup/Caddyfile.before" /etc/caddy/Caddyfile
  systemctl reload caddy >/dev/null 2>&1 || true
  echo "London community installation failed; Caddy was restored. Evidence: $backup" >&2
 fi
 exit "$status"
}
trap cleanup EXIT HUP INT TERM

install -d -o root -g root -m 0755 /opt/bitcoinwalk-chat-london
install -d -o root -g root -m 0700 /etc/bitcoinwalk-chat-london
install -o root -g root -m 0755 "$binary" /opt/bitcoinwalk-chat-london/bitcoinwalk-relay
printf '%s\n' 'RELAY_CHAT_URL=wss://chat-london.bitcoinwalk.org' > /etc/bitcoinwalk-chat-london/public.conf
chmod 0644 /etc/bitcoinwalk-chat-london/public.conf
RELAY_CHAT_KEY_INIT=/etc/bitcoinwalk-chat-london/relay-key /opt/bitcoinwalk-chat-london/bitcoinwalk-relay
chmod 0600 /etc/bitcoinwalk-chat-london/relay-key
cp -p /etc/bitcoinwalk-chat-london/relay-key "$backup/relay-key.recovery"
install -o root -g root -m 0644 "deploy/$unit" "$target_unit"

candidate="$backup/Caddyfile.candidate"
cp -p /etc/caddy/Caddyfile "$candidate"
printf '\n' >>"$candidate"
cat "deploy/$caddy_snippet" >>"$candidate"
caddy validate --config "$candidate" --adapter caddyfile
install -o root -g root -m 0644 "$candidate" /etc/caddy/Caddyfile

systemctl daemon-reload
systemctl enable --now bitcoinwalk-chat-london.service
systemctl reload caddy

attempt=0
until curl -fsS --max-time 5 http://127.0.0.1:3346/healthz >"$backup/health.json"; do
 attempt=$((attempt+1)); test "$attempt" -lt 20 || exit 1; sleep 1
done
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3346/ >"$backup/nip11.json"
python3 - "$backup/nip11.json" <<'PY'
import json,re,sys
data=json.load(open(sys.argv[1],encoding="utf-8"))
assert 29 in data.get("supported_nips",[]), data
assert re.fullmatch(r"[0-9a-f]{64}",data.get("self", "")), data
assert data.get("name")=="BitcoinWalk London Community", data
PY
systemctl is-active --quiet bitcoinwalk-chat-london.service
systemctl is-active --quiet bitcoinwalk-chat.service
curl -fsS --max-time 5 http://127.0.0.1:3339/healthz >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:3338/api/healthz >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:3345/api/healthz >/dev/null

sha256sum /opt/bitcoinwalk-chat-london/bitcoinwalk-relay "$target_unit" /etc/caddy/Caddyfile >"$backup/installed.sha256"
trap - EXIT HUP INT TERM
echo "London dedicated community transport 0.8.96 prepared. Evidence: $backup"
echo "Loopback relay: 127.0.0.1:3346; public endpoint: wss://chat-london.bitcoinwalk.org/"
echo "No group was created and no web routing was activated. Preserve an offline copy of $backup/relay-key.recovery."
