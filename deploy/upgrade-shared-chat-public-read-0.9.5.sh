#!/usr/bin/env bash
set -euo pipefail

test "$(id -u)" -eq 0 || { echo "Run with sudo." >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c SHARED-CHAT-PUBLIC-READ-0.9.5-SHA256SUMS

service=bitcoinwalk-chat.service
target=/opt/bitcoinwalk-chat/bitcoinwalk-relay
unit=/etc/systemd/system/bitcoinwalk-chat.service
config=/etc/bitcoinwalk-chat/public.conf
database=/var/lib/bitcoinwalk-chat/chat.db
credential=/etc/bitcoinwalk-chat/relay-key
artifact="$PWD/bitcoinwalk-chat-shared-0.9.5"
metadata_audit="$PWD/chat-public-audit-0.9.1"
visibility_audit="$PWD/chat-visibility-audit-0.9.5"
self=019f33d28e46ecf5c70ebd478c8dd6b6475cdd9eccdc7f1c533afd0eae8dc713
public_group=13bc3a423b4f2954
private_group=bitcoinwalk-organizers

test "$(sha256sum "$target" | cut -d ' ' -f 1)" = e3514cf214e4261a19aa48533ba19390b67a186468dbd9c423fc4cd4b7aa24a8 || { echo "Unexpected installed chat binary; stop for review." >&2; exit 1; }
test "$(sha256sum "$unit" | cut -d ' ' -f 1)" = 98d21d1fdccf9dbcbc955ab6f1909b240ab2bc41ca340d38f3ab0c7f970a82ef || { echo "Unexpected chat service contract; stop for review." >&2; exit 1; }
test "$(sha256sum "$config" | cut -d ' ' -f 1)" = dd7a61a18b9cc6052b359e858116fabf6e34707ba92b45d39ccd5a30a9fcea86 || { echo "Unexpected public chat configuration; stop for review." >&2; exit 1; }
grep -Fx 'RELAY_CHAT_URL=wss://chat.bitcoinwalk.org' "$config" >/dev/null
systemctl is-active --quiet "$service"
curl -fsS --max-time 5 http://127.0.0.1:3339/healthz >/dev/null

backup=$(mktemp -d /var/backups/bitcoinwalk-chat-public-read.XXXXXX)
chmod 0700 "$backup"
cp -a "$target" "$backup/bitcoinwalk-relay.before"
cp -a "$unit" "$backup/bitcoinwalk-chat.service.before"
cp -a "$config" "$backup/public.conf.before"
cp -a "$credential" "$backup/relay-key.recovery"
"$metadata_audit" -relay wss://chat.bitcoinwalk.org/ -group "$public_group" -relay-self "$self" >"$backup/public.before.json"

systemctl stop "$service"
cp -a "$database" "$backup/chat.pre-rehearsal.db"
systemctl start "$service"
curl -fsS --retry 10 --retry-delay 1 --retry-all-errors --max-time 5 http://127.0.0.1:3339/healthz >/dev/null

cp -a "$backup/chat.pre-rehearsal.db" "$backup/chat.candidate.db"
candidate_pid=
replacement_started=0
accepted=0
cleanup() {
  if test -n "$candidate_pid"; then kill "$candidate_pid" 2>/dev/null || true; wait "$candidate_pid" 2>/dev/null || true; fi
  if test "$replacement_started" -eq 1 && test "$accepted" -ne 1; then
    systemctl stop "$service" || true
    install -o root -g root -m 0755 "$backup/bitcoinwalk-relay.before" "$target"
    cp -a "$backup/chat.pre-activation.db" "$database"
    systemctl start "$service" || true
    echo "Shared chat public-read upgrade failed; prior binary and database restored. Backup: $backup" >&2
  fi
}
trap cleanup EXIT

env RELAY_CHAT_MODE=production RELAY_CHAT_LISTEN=127.0.0.1:3399 \
  RELAY_CHAT_DB="$backup/chat.candidate.db" RELAY_CHAT_KEY_FILE="$backup/relay-key.recovery" \
  RELAY_CHAT_URL=wss://chat.bitcoinwalk.org "$artifact" >"$backup/candidate.log" 2>&1 &
candidate_pid=$!
curl -fsS --retry 10 --retry-delay 1 --retry-all-errors --max-time 5 http://127.0.0.1:3399/healthz >/dev/null
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3399/ >"$backup/candidate.nip11.json"
python3 - "$backup/candidate.nip11.json" "$self" <<'PY'
import json,sys
d=json.load(open(sys.argv[1],encoding="utf-8"))
assert d.get("self")==sys.argv[2], d
assert {1,11,29,42}.issubset(set(d.get("supported_nips",[]))), d
assert d.get("version")=="bitcoinwalk-chat-shared-0.9.5", d
PY
"$metadata_audit" -relay ws://127.0.0.1:3399/ -group "$public_group" -relay-self "$self" >"$backup/candidate.public.json"
cmp "$backup/public.before.json" "$backup/candidate.public.json"
"$visibility_audit" -relay ws://127.0.0.1:3399/ -public-group "$public_group" -private-group "$private_group" >"$backup/candidate.visibility.json"
kill "$candidate_pid"; wait "$candidate_pid" || true; candidate_pid=

systemctl stop "$service"
cp -a "$database" "$backup/chat.pre-activation.db"
RELAY_REPLICA_DB_DIGEST="$backup/chat.pre-activation.db" "$artifact" >"$backup/chat.pre-activation.logical-digest"
install -o root -g root -m 0755 "$artifact" "$target"
replacement_started=1
systemctl start "$service"
curl -fsS --retry 10 --retry-delay 1 --retry-all-errors --max-time 5 http://127.0.0.1:3339/healthz >/dev/null

verify_live() {
  curl -fsS --max-time 10 -H 'Accept: application/nostr+json' https://chat.bitcoinwalk.org/ >"$1"
  python3 - "$1" "$self" <<'PY'
import json,sys
d=json.load(open(sys.argv[1],encoding="utf-8"))
assert d.get("self")==sys.argv[2], d
assert d.get("version")=="bitcoinwalk-chat-shared-0.9.5", d
PY
}
verify_live "$backup/public.after.json"
"$metadata_audit" -relay wss://chat.bitcoinwalk.org/ -group "$public_group" -relay-self "$self" >"$backup/public-metadata.after.json"
cmp "$backup/public.before.json" "$backup/public-metadata.after.json"
"$visibility_audit" -relay wss://chat.bitcoinwalk.org/ -public-group "$public_group" -private-group "$private_group" >"$backup/live.visibility.json"

systemctl stop "$service"
RELAY_CHAT_DB="$database" RELAY_CHAT_KEY_FILE="$credential" RELAY_CHAT_BACKUP="$backup/chat.activated.db" "$target"
RELAY_REPLICA_DB_DIGEST="$backup/chat.activated.db" "$artifact" >"$backup/chat.activated.logical-digest"
cmp "$backup/chat.pre-activation.logical-digest" "$backup/chat.activated.logical-digest"
systemctl start "$service"
curl -fsS --retry 10 --retry-delay 1 --retry-all-errors --max-time 5 http://127.0.0.1:3339/healthz >/dev/null
verify_live "$backup/public.after-restart.json"
"$visibility_audit" -relay wss://chat.bitcoinwalk.org/ -public-group "$public_group" -private-group "$private_group" >"$backup/restart.visibility.json"
systemctl is-active --quiet "$service"

( cd "$backup" && sha256sum bitcoinwalk-relay.before bitcoinwalk-chat.service.before public.conf.before relay-key.recovery chat.pre-rehearsal.db chat.pre-activation.db chat.activated.db chat.pre-activation.logical-digest chat.activated.logical-digest public.before.json candidate.public.json candidate.visibility.json live.visibility.json restart.visibility.json >SHA256SUMS )
accepted=1
trap - EXIT
echo "Shared chat public-read policy accepted on relay 0.9.5. Backup: $backup"
echo "City and Global history are publicly readable; bitcoinwalk-organizers remains membership-only."
