#!/bin/sh
# Add NIP-29 group picture metadata to the shared staging community relay.
# Existing groups, members, messages, relay identity and signing key are retained.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo on the relay host.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-city-groups-0.9.1
target=/opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-chat-staging/chat.db
key=/etc/bitcoinwalk-chat-staging/relay-key
unit=bitcoinwalk-chat-staging
endpoint=https://chat-staging.bitcoinwalk.org/
predecessor=8a4a656573ea92f7af9cda3d3fdbfc58d750386bd2e12ad4751b536b8eda7dc4

sha256sum -c CITY-GROUP-AVATARS-0.9.1-SHA256SUMS
test "$(sha256sum "$target" | cut -d ' ' -f 1)" = "$predecessor" || {
  echo 'Installed shared chat relay is not the reviewed predecessor; nothing changed.' >&2
  exit 1
}
test -f "$database" && test ! -L "$database"
test -f "$key" && test ! -L "$key"
systemctl is-active --quiet "$unit"

backup=$(mktemp -d /var/backups/bitcoinwalk-city-group-avatars.XXXXXX)
chmod 0700 "$backup"
cp -p "$target" "$backup/bitcoinwalk-relay.before"
cp -p "$key" "$backup/relay-key.recovery"
cp -p /etc/systemd/system/bitcoinwalk-chat-staging.service "$backup/service.before"
curl -fsS --max-time 10 -H 'Accept: application/nostr+json' "$endpoint" >"$backup/nip11.before.json"

changed=0
complete=0
rollback() {
  code=$?
  trap - EXIT HUP INT TERM
  if test "$changed" -eq 1; then
    systemctl stop "$unit" >/dev/null 2>&1 || true
    install -o root -g root -m 0755 "$backup/bitcoinwalk-relay.before" "$target"
    systemctl start "$unit" >/dev/null 2>&1 || true
  fi
  test "$complete" -eq 1 || echo "Shared chat upgrade did not complete; predecessor restored and live database retained. Backup: $backup" >&2
  exit "$code"
}
trap rollback EXIT HUP INT TERM

systemctl stop "$unit"
RELAY_CHAT_DB="$database" RELAY_CHAT_KEY_FILE="$key" RELAY_CHAT_BACKUP="$backup/chat.before.db" "$target"
changed=1
install -o root -g root -m 0755 "$artifact" "$target"
systemctl start "$unit"

attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3335/healthz >"$backup/health.activated.json"; do
  attempt=$((attempt+1))
  test "$attempt" -lt 20 || exit 1
  sleep 1
done
curl -fsS --max-time 10 -H 'Accept: application/nostr+json' "$endpoint" >"$backup/nip11.activated.json"
python3 - "$backup/nip11.before.json" "$backup/nip11.activated.json" <<'PY'
import json,sys
before=json.load(open(sys.argv[1],encoding="utf-8"))
after=json.load(open(sys.argv[2],encoding="utf-8"))
if before.get("self") != after.get("self"):
    raise SystemExit("relay identity changed")
if not {1,11,29,42}.issubset(set(after.get("supported_nips",[]))):
    raise SystemExit("required NIPs are not advertised")
PY

systemctl stop "$unit"
RELAY_CHAT_DB="$database" RELAY_CHAT_KEY_FILE="$key" RELAY_CHAT_BACKUP="$backup/chat.activated.db" "$target"
cmp "$backup/chat.before.db" "$backup/chat.activated.db"
systemctl start "$unit"

attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3335/healthz >"$backup/health.restart.json"; do
  attempt=$((attempt+1))
  test "$attempt" -lt 20 || exit 1
  sleep 1
done
curl -fsS --max-time 10 -H 'Accept: application/nostr+json' "$endpoint" >"$backup/nip11.restart.json"
python3 - "$backup/nip11.activated.json" "$backup/nip11.restart.json" <<'PY'
import json,sys
activated=json.load(open(sys.argv[1],encoding="utf-8"))
restart=json.load(open(sys.argv[2],encoding="utf-8"))
if activated.get("self") != restart.get("self"):
    raise SystemExit("relay identity changed after restart")
if not {1,11,29,42}.issubset(set(restart.get("supported_nips",[]))):
    raise SystemExit("required NIPs disappeared after restart")
PY

systemctl is-active --quiet "$unit"
(
  cd "$backup"
  sha256sum bitcoinwalk-relay.before relay-key.recovery service.before chat.before.db chat.activated.db \
    nip11.before.json nip11.activated.json nip11.restart.json health.activated.json health.restart.json >SHA256SUMS
)
complete=1
trap - EXIT HUP INT TERM
echo "Shared city-group avatar support 0.9.1 accepted. Backup: $backup"
echo 'Relay identity, signing key and exact persisted chat state were retained across activation and restart.'
echo 'No group, member or message was created by this upgrade.'
