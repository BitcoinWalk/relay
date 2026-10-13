#!/bin/sh
set -eu

test "$(id -u)" -eq 0 || { echo "Run with sudo." >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-chat-london.<id>" >&2; exit 1; }
failed_backup=$1
case "$failed_backup" in /var/backups/bitcoinwalk-chat-london.[A-Za-z0-9]*) ;; *) echo "Use the exact failed-install backup path." >&2; exit 1;; esac
test -d "$failed_backup"
cd "$(dirname "$0")/.."
sha256sum -c CHAT-LONDON-RESUME-0.8.97-SHA256SUMS

old_unit_hash=f99651280c9bd8dd8091a45102942baca1818013fcf52e3edfa01a159fccbbfe
binary_hash=3275bb27531eeef174d2d62c1f89735dffea2a81ac98d738acc16ac1623aac00
test "$(sha256sum /opt/bitcoinwalk-chat-london/bitcoinwalk-relay | cut -d ' ' -f 1)" = "$binary_hash"
test "$(sha256sum /etc/systemd/system/bitcoinwalk-chat-london.service | cut -d ' ' -f 1)" = "$old_unit_hash"
test -s /etc/bitcoinwalk-chat-london/relay-key
test "$(cat /etc/bitcoinwalk-chat-london/public.conf)" = 'RELAY_CHAT_URL=wss://chat-london.bitcoinwalk.org'
test -f "$failed_backup/Caddyfile.before"
cmp -s "$failed_backup/Caddyfile.before" /etc/caddy/Caddyfile
! grep -q 'chat-london\.bitcoinwalk\.org' /etc/caddy/Caddyfile
! systemctl is-active --quiet bitcoinwalk-chat-london.service
! systemctl is-enabled --quiet bitcoinwalk-chat-london.service
! ss -ltnH '( sport = :3346 )' | grep -q .
systemctl is-active --quiet bitcoinwalk-chat.service
curl -fsS --max-time 5 http://127.0.0.1:3339/healthz >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:3338/api/healthz >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:3345/api/healthz >/dev/null

backup=$(mktemp -d /var/backups/bitcoinwalk-chat-london-resume.XXXXXX)
chmod 0700 "$backup"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile.before"
cp -p /etc/systemd/system/bitcoinwalk-chat-london.service "$backup/service.before"
cp -p /etc/bitcoinwalk-chat-london/relay-key "$backup/relay-key.recovery"

cleanup() {
 status=$?
 if test "$status" -ne 0; then
  systemctl disable --now bitcoinwalk-chat-london.service >/dev/null 2>&1 || true
  cp -p "$backup/service.before" /etc/systemd/system/bitcoinwalk-chat-london.service
  cp -p "$backup/Caddyfile.before" /etc/caddy/Caddyfile
  systemctl daemon-reload >/dev/null 2>&1 || true
  systemctl reload caddy >/dev/null 2>&1 || true
  echo "London community resume failed; failed-install state was restored. Evidence: $backup" >&2
 fi
 exit "$status"
}
trap cleanup EXIT HUP INT TERM

install -o root -g root -m 0644 deploy/bitcoinwalk-chat-london-0.8.97.service /etc/systemd/system/bitcoinwalk-chat-london.service
candidate="$backup/Caddyfile.candidate"
cp -p /etc/caddy/Caddyfile "$candidate"
printf '\n' >>"$candidate"
cat deploy/chat-london-0.8.96.caddy >>"$candidate"
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
assert data.get("name")=="BitcoinWalk London Community", data
assert 29 in data.get("supported_nips",[]), data
assert re.fullmatch(r"[0-9a-f]{64}",data.get("self", "")), data
PY
systemctl is-active --quiet bitcoinwalk-chat-london.service
systemctl is-active --quiet bitcoinwalk-chat.service
curl -fsS --max-time 5 http://127.0.0.1:3339/healthz >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:3338/api/healthz >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:3345/api/healthz >/dev/null
sha256sum /opt/bitcoinwalk-chat-london/bitcoinwalk-relay /etc/systemd/system/bitcoinwalk-chat-london.service /etc/caddy/Caddyfile >"$backup/installed.sha256"

trap - EXIT HUP INT TERM
echo "London dedicated community transport resume accepted on 0.8.97. Evidence: $backup"
echo "The original relay identity and empty database were preserved; global chat and both web apps remained healthy."
echo "No group was created and no BitcoinWalk web routing was activated. Preserve an offline copy of $backup/relay-key.recovery."
