#!/bin/sh
# Correct the empty London combined relay so newly created Walkie Talkie groups
# advertise public/open-join discovery metadata. No group is created here.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on .240.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-london-combined-activation.<id>" >&2; exit 1; }
accepted=$1
case "$accepted" in /var/backups/bitcoinwalk-london-combined-activation.[A-Za-z0-9]*) ;; *) echo 'Use the exact accepted 0.8.99 activation backup path.' >&2; exit 1;; esac
cd "$(dirname "$0")/.."

manifest=LONDON-PUBLIC-WALKIE-TALKIE-0.9.0-SHA256SUMS
artifact=./bitcoinwalk-relay-london-combined-0.9.0
audit=./replica-audit-london-combined-0.9.0
probe=./combined-chat-probe-0.9.0
target=/opt/bitcoinwalk-relay-london
state=/var/lib/bitcoinwalk-relay-london
container=bitcoinwalk-relay-london
project=bitcoinwalk-relay-london
image=bitcoinwalk-relay-london:0.9.0
endpoint=wss://london.bitcoinwalk.org/
city=ca2f9905-fb4d-4948-a12c-c792b28ec7c8

wait_public() { attempt=0; until curl -fsS --max-time 2 https://london.bitcoinwalk.org/healthz >/dev/null; do attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1; done; }
digest() { RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }
audit_exact() {
  "$audit" -source "$endpoint" -replica "$endpoint" -city "$city" >"$1"
  python3 - "$1" <<'PY'
import json,sys
r=json.load(open(sys.argv[1],encoding="utf-8"))
ids=r["source"]["occurrenceIds"]
if not ids or r["replica"]["occurrenceIds"] != ids or not r.get("exactEventIds"):
    raise SystemExit("London events are not exact")
if any(not r[x].get("allSignaturesValid") or r[x].get("privateWrapperCount") for x in ("source","replica")):
    raise SystemExit("London event signature/privacy audit failed")
PY
}
capture_nip11() {
  curl -fsS --max-time 5 -H 'Accept: application/nostr+json' https://london.bitcoinwalk.org/ >"$1"
  python3 - "$1" "$accepted/nip11.activated.json" <<'PY'
import json,sys
live=json.load(open(sys.argv[1],encoding="utf-8")); old=json.load(open(sys.argv[2],encoding="utf-8"))
if live.get("version") != "bitcoinwalk-city-community-relay-0.9.0" or live.get("self") != old.get("self"):
    raise SystemExit("London relay version or identity is unexpected")
if not {1,11,29,42}.issubset(set(live.get("supported_nips",[]))):
    raise SystemExit("required NIPs missing")
PY
}

sha256sum -c "$manifest"
test -d "$accepted" && (cd "$accepted" && sha256sum -c SHA256SUMS >/dev/null)
test -s "$state/events.db" && test -s "$state/chat.db" && test -s "$state/chat-relay-key"
test "$(docker inspect "$container" --format '{{.Config.Image}}')" = bitcoinwalk-relay-london:0.8.99
test -z "$(docker port "$container")"
if docker image inspect "$image" >/dev/null 2>&1; then echo "$image already exists; stop for review." >&2; exit 1; fi
wait_public

backup=$(mktemp -d /var/backups/bitcoinwalk-london-public-walkie-talkie.XXXXXX)
chmod 0700 "$backup"
cp -p "$target/bitcoinwalk-relay" "$backup/bitcoinwalk-relay.before"
cp -p "$target/Dockerfile" "$backup/Dockerfile.before"
cp -p "$target/compose.yaml" "$backup/compose.before.yaml"
cp -p "$state/events.db" "$backup/events.db"
cp -p "$state/chat.db" "$backup/chat.db"
cp -p "$state/chat-relay-key" "$backup/chat-relay-key.recovery"
digest "$state/events.db" >"$backup/event-digest.before"
digest "$state/chat.db" >"$backup/chat-digest.before"
audit_exact "$backup/events.before.json"
"$probe" -relay "$endpoint" >"$backup/chat.before.json"
completed=0; changed=0
rollback() {
  code=$?; trap - EXIT HUP INT TERM
  if test "$changed" -eq 1; then
    docker compose -p "$project" -f "$target/compose.yaml" down >/dev/null 2>&1 || true
    install -o root -g root -m 0555 "$backup/bitcoinwalk-relay.before" "$target/bitcoinwalk-relay"
    install -o root -g root -m 0444 "$backup/Dockerfile.before" "$target/Dockerfile"
    install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target/compose.yaml"
    install -o 65532 -g 65532 -m 0600 "$backup/events.db" "$state/events.db"
    install -o 65532 -g 65532 -m 0600 "$backup/chat.db" "$state/chat.db"
    install -o 65532 -g 65532 -m 0600 "$backup/chat-relay-key.recovery" "$state/chat-relay-key"
    docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null 2>&1 || true
  fi
  docker image rm "$image" >/dev/null 2>&1 || true
  test "$completed" -eq 1 || echo "London public Walkie Talkie upgrade did not complete; combined relay 0.8.99 was restored. Backup: $backup" >&2
  exit "$code"
}
trap rollback EXIT HUP INT TERM

docker stop -t 20 "$container" >/dev/null
changed=1
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 deploy/Dockerfile.london-combined-0.9.0 "$target/Dockerfile"
install -o root -g root -m 0444 deploy/compose.london-combined-0.9.0.yaml "$target/compose.yaml"
docker build --pull=false --network=none -t "$image" "$target" >"$backup/docker-build.log"
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null
wait_public
capture_nip11 "$backup/nip11.activated.json"
audit_exact "$backup/events.activated.json"
"$probe" -relay "$endpoint" >"$backup/chat.activated.json"
digest "$state/events.db" >"$backup/event-digest.activated"
digest "$state/chat.db" >"$backup/chat-digest.activated"
cmp "$backup/event-digest.before" "$backup/event-digest.activated"
cmp "$backup/chat-digest.before" "$backup/chat-digest.activated"

docker restart "$container" >/dev/null
wait_public
capture_nip11 "$backup/nip11.restart.json"
audit_exact "$backup/events.restart.json"
"$probe" -relay "$endpoint" >"$backup/chat.restart.json"
digest "$state/events.db" >"$backup/event-digest.restart"
digest "$state/chat.db" >"$backup/chat-digest.restart"
cmp "$backup/event-digest.before" "$backup/event-digest.restart"
cmp "$backup/chat-digest.before" "$backup/chat-digest.restart"
test -z "$(docker port "$container")"
docker inspect "$container" --format '{{.Config.User}} {{.HostConfig.ReadonlyRootfs}}' | grep -qx '65532:65532 true'
(
  cd "$backup"
  sha256sum bitcoinwalk-relay.before Dockerfile.before compose.before.yaml events.db chat.db chat-relay-key.recovery event-digest.before event-digest.activated event-digest.restart chat-digest.before chat-digest.activated chat-digest.restart events.before.json events.activated.json events.restart.json chat.before.json chat.activated.json chat.restart.json nip11.activated.json nip11.restart.json docker-build.log >SHA256SUMS
)
completed=1
trap - EXIT HUP INT TERM
echo "London public/open-join Walkie Talkie policy accepted on 0.9.0. Backup: $backup"
echo "The relay identity, empty chat state and exact city event set remained unchanged across activation and restart."
echo "No group was created; the super-admin can now create London through the application workflow."
