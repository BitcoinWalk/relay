#!/bin/sh
# Activate NIP-29 on the canonical Bangkok event receiver using the exact
# identity accepted by the isolated 0.9.14 rehearsal. No group is created.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on .240.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-bangkok-combined-rehearsal.<id>" >&2; exit 1; }
rehearsal=$1
case "$rehearsal" in /var/backups/bitcoinwalk-bangkok-combined-rehearsal.[A-Za-z0-9]*) ;; *) echo 'Use the exact accepted rehearsal backup path.' >&2; exit 1;; esac
cd "$(dirname "$0")/.."

manifest=BANGKOK-COMBINED-0.9.14-SHA256SUMS
artifact=./bitcoinwalk-relay-bangkok-combined-0.9.14
audit=./replica-audit-bangkok-combined-0.9.14
probe=./combined-chat-probe-0.9.14
dockerfile=./deploy/Dockerfile.bangkok-combined-0.9.14
compose_source=./deploy/compose.bangkok-combined-0.9.14.yaml
target=/opt/bitcoinwalk-city-relays/bangkok
state=/var/lib/bitcoinwalk-city-relays/bangkok
event_db=$state/events.db
chat_db=$state/chat.db
chat_key=$state/chat-relay-key
container=bitcoinwalk-relay-bangkok
project=bitcoinwalk-relay-bangkok
image=bitcoinwalk-city-community-relay-bangkok:0.9.14
network=root_my_custom_network
city=d7d1cda8-7a58-42ad-ab60-c4bad61dab9a
endpoint=wss://bangkok.bitcoinwalk.org/
https_endpoint=https://bangkok.bitcoinwalk.org/

wait_public() {
  attempt=0
  until curl -fsS --max-time 2 https://bangkok.bitcoinwalk.org/healthz >/dev/null; do
    attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1
  done
}
audit_exact() {
  output=$1
  "$audit" -source "$endpoint" -replica "$endpoint" -city "$city" >"$output"
  python3 - "$output" "$city" <<'PY'
import json,sys
report=json.load(open(sys.argv[1],encoding="utf-8"))
events=report["source"]["occurrenceIds"]
if report.get("cityId") != sys.argv[2] or not events or report["replica"]["occurrenceIds"] != events or not report.get("exactEventIds"):
    raise SystemExit("Bangkok event state is not exact")
for side in ("source","replica"):
    if not report[side].get("allSignaturesValid") or report[side].get("privateWrapperCount") != 0:
        raise SystemExit("Bangkok signature or privacy audit failed")
PY
}
capture_nip11() {
  output=$1
  curl -fsS --max-time 5 -H 'Accept: application/nostr+json' "$https_endpoint" >"$output"
  python3 - "$output" "$rehearsal/candidate-nip11.first.json" <<'PY'
import json,re,sys
live=json.load(open(sys.argv[1],encoding="utf-8"))
accepted=json.load(open(sys.argv[2],encoding="utf-8"))
if live.get("name") != "BitcoinWalk Bangkok relay" or live.get("version") != "bitcoinwalk-city-community-relay-0.9.14":
    raise SystemExit("unexpected live combined-relay NIP-11 identity")
if not {1,11,29,42}.issubset(set(live.get("supported_nips",[]))):
    raise SystemExit("live combined relay does not advertise the required NIPs")
if not re.fullmatch(r"[0-9a-f]{64}",live.get("self", "")) or live.get("self") != accepted.get("self"):
    raise SystemExit("live NIP-29 identity differs from the rehearsed identity")
PY
}
verify_topology() {
  output=$1
  docker inspect "$container" >"$output"
  python3 - "$output" "$network" <<'PY'
import json,sys
container=json.load(open(sys.argv[1],encoding="utf-8"))[0]
if not container["State"]["Running"] or container["Config"]["User"] != "65532:65532" or not container["HostConfig"]["ReadonlyRootfs"]:
    raise SystemExit("Bangkok runtime identity or hardening changed")
if container["HostConfig"].get("PortBindings"):
    raise SystemExit("Bangkok relay unexpectedly publishes a host port")
if set(container["HostConfig"].get("Tmpfs",{})) != {"/tmp"}:
    raise SystemExit("Bangkok hardened tmpfs is unavailable")
if set(container["NetworkSettings"]["Networks"]) != {sys.argv[2]}:
    raise SystemExit("Bangkok Docker network changed")
PY
}

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp install cp chown chmod sync cmp grep; do command -v "$command" >/dev/null; done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
test -x "$artifact" && test -x "$audit" && test -x "$probe"
test -d "$rehearsal" && test -f "$rehearsal/SHA256SUMS" && test -s "$rehearsal/chat-relay-key.recovery" && test -f "$rehearsal/candidate-nip11.first.json"
(cd "$rehearsal" && sha256sum -c SHA256SUMS >/dev/null)
test -f "$target/manifest.json" && test -f "$target/manifest.sha256" && test -f "$target/compose.yaml" && test -f "$event_db"
test ! -e "$target/bitcoinwalk-relay" && test ! -e "$target/Dockerfile"
test ! -e "$chat_db" && test ! -e "$chat_key"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$container" --format '{{.Config.Image}}')" = bitcoinwalk-city-relay:0.8.92
test -z "$(docker port "$container")"
if docker image inspect "$image" >/dev/null 2>&1; then echo "$image already exists; stop for review." >&2; exit 1; fi
wait_public

backup=$(mktemp -d /var/backups/bitcoinwalk-bangkok-combined-activation.XXXXXX)
chmod 0700 "$backup"
cp -p "$target/compose.yaml" "$backup/compose.before.yaml"
cp -p "$target/manifest.json" "$backup/manifest.before.json"
cp -p "$target/manifest.sha256" "$backup/manifest.before.sha256"
docker inspect "$container" >"$backup/container.before.json"
docker logs "$container" >"$backup/container.before.log" 2>&1
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' "$https_endpoint" >"$backup/nip11.before.json"
audit_exact "$backup/events.before.json"
completed=0
changed=0
image_built=0
container_stopped=0
rollback() {
  code=$?; trap - EXIT HUP INT TERM
  if test "$container_stopped" -eq 1; then docker start "$container" >/dev/null 2>&1 || true; fi
  if test "$changed" -eq 1; then
    docker compose -p "$project" -f "$target/compose.yaml" down >/dev/null 2>&1 || true
    install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target/compose.yaml"
    install -o 65532 -g 65532 -m 0600 "$backup/events.db" "$event_db"
    rm -f "$target/bitcoinwalk-relay" "$target/Dockerfile" "$chat_db" "$chat_key"
    docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null 2>&1 || true
  fi
  test "$image_built" -eq 0 || docker image rm "$image" >/dev/null 2>&1 || true
  if test "$completed" -ne 1; then echo "Bangkok combined-relay activation did not complete; the accepted event-only relay was restored. Backup: $backup" >&2; fi
  exit "$code"
}
trap rollback EXIT HUP INT TERM

docker stop -t 20 "$container" >/dev/null
container_stopped=1
RELAY_REPLICA_DB_DIGEST="$event_db" "$artifact" >"$backup/event-digest.before"
cp -p "$event_db" "$backup/events.db"
sync "$backup/events.db"
changed=1
install -o 65532 -g 65532 -m 0600 "$rehearsal/chat-relay-key.recovery" "$chat_key"
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile" "$target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$target/compose.yaml"
docker build --pull=false --network=none -t "$image" "$target" >"$backup/docker-build.log"
image_built=1
docker compose -p "$project" -f "$target/compose.yaml" config >"$backup/compose.resolved.yaml"
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null
container_stopped=0
wait_public
verify_topology "$backup/container.activated.json"
capture_nip11 "$backup/nip11.activated.json"
audit_exact "$backup/events.activated.json"
cmp "$backup/events.before.json" "$backup/events.activated.json"
"$probe" -relay "$endpoint" >"$backup/chat.activated.json"

docker stop -t 20 "$container" >/dev/null
container_stopped=1
RELAY_REPLICA_DB_DIGEST="$event_db" "$artifact" >"$backup/event-digest.activated"
RELAY_REPLICA_DB_DIGEST="$chat_db" "$artifact" >"$backup/chat-digest.activated"
cp -p "$chat_db" "$backup/chat.activated.db"
docker start "$container" >/dev/null
container_stopped=0
wait_public
capture_nip11 "$backup/nip11.restart.json"
audit_exact "$backup/events.restart.json"
"$probe" -relay "$endpoint" >"$backup/chat.restart.json"
cmp "$backup/nip11.activated.json" "$backup/nip11.restart.json"
cmp "$backup/events.before.json" "$backup/events.restart.json"
cmp "$backup/chat.activated.json" "$backup/chat.restart.json"

docker stop -t 20 "$container" >/dev/null
container_stopped=1
RELAY_REPLICA_DB_DIGEST="$event_db" "$artifact" >"$backup/event-digest.restart"
RELAY_REPLICA_DB_DIGEST="$chat_db" "$artifact" >"$backup/chat-digest.restart"
cp -p "$chat_db" "$backup/chat.restart.db"
cp -p "$chat_key" "$backup/chat-relay-key.recovery"
cmp "$backup/event-digest.before" "$backup/event-digest.activated"
cmp "$backup/event-digest.before" "$backup/event-digest.restart"
cmp "$backup/chat-digest.activated" "$backup/chat-digest.restart"
docker start "$container" >/dev/null
container_stopped=0
wait_public
verify_topology "$backup/container.accepted.json"
audit_exact "$backup/events.accepted.json"
cmp "$backup/events.before.json" "$backup/events.accepted.json"
test -z "$(docker port "$container")"
docker logs "$container" >"$backup/container.accepted.log" 2>&1
grep -q 'Combined city event and NIP-29 community relay enabled with isolated stores' "$backup/container.accepted.log"
(
  cd "$backup"
  sha256sum compose.before.yaml manifest.before.json manifest.before.sha256 events.db chat.activated.db chat.restart.db chat-relay-key.recovery event-digest.before event-digest.activated event-digest.restart chat-digest.activated chat-digest.restart events.before.json events.activated.json events.restart.json events.accepted.json chat.activated.json chat.restart.json nip11.before.json nip11.activated.json nip11.restart.json container.before.json container.activated.json container.accepted.json container.before.log container.accepted.log docker-build.log compose.resolved.yaml >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Canonical Bangkok event/NIP-29 relay activation accepted on 0.9.14. Backup: $backup"
echo "Bangkok remained exact across activation and restart; its event and NIP-29 stores are isolated and logically unchanged."
echo "The rehearsed relay identity is active, unauthorized group creation remains denied and no group was created."
echo "No DNS, proxy, replication registry or signed-directory state was changed. Preserve an offline copy of $backup/chat-relay-key.recovery."
