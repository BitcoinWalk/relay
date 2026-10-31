#!/bin/sh
# Rehearse the Bangkok event/NIP-29 combined relay in an isolated container.
# The public Bangkok container is stopped only long enough to take a consistent
# event-database copy, then restored before the candidate starts.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on .240.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=BANGKOK-COMBINED-0.9.15-SHA256SUMS
artifact=./bitcoinwalk-relay-bangkok-combined-0.9.15
audit=./replica-audit-bangkok-combined-0.9.15
probe=./combined-chat-probe-0.9.15
dockerfile=./deploy/Dockerfile.bangkok-combined-0.9.15
compose_source=./deploy/compose.bangkok-combined-rehearsal-0.9.15.yaml
live_container=bitcoinwalk-relay-bangkok
live_state=/var/lib/bitcoinwalk-city-relays/bangkok/events.db
live_endpoint=wss://bangkok.bitcoinwalk.org/
live_https=https://bangkok.bitcoinwalk.org/
candidate_container=bitcoinwalk-relay-bangkok-combined-rehearsal
candidate_target=/opt/bitcoinwalk-city-relays/bangkok-combined-rehearsal
candidate_state=/var/lib/bitcoinwalk-city-relays/bangkok-combined-rehearsal
candidate_image=bitcoinwalk-relay-bangkok-combined-rehearsal:0.9.15
candidate_project=bitcoinwalk-relay-bangkok-combined-rehearsal
network=root_my_custom_network
city=d7d1cda8-7a58-42ad-ab60-c4bad61dab9a
port=3347

wait_live() {
  attempt=0
  until curl -fsS --max-time 2 https://bangkok.bitcoinwalk.org/healthz >/dev/null; do
    attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1
  done
}
candidate_ip() {
  ip=$(docker inspect "$candidate_container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}')
  python3 - "$ip" <<'PY'
import ipaddress,sys
address=ipaddress.ip_address(sys.argv[1])
if address.version != 4 or not address.is_private:
    raise SystemExit("candidate did not receive a private IPv4 address")
PY
  printf '%s\n' "$ip"
}
wait_candidate() {
  attempt=0
  until ip=$(candidate_ip) && curl -fsS --max-time 2 "http://$ip:$port/healthz" >/dev/null; do
    attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1
  done
}
audit_exact() {
  replica=$1 output=$2
  "$audit" -source "$live_endpoint" -replica "$replica" -city "$city" -allow-empty >"$output"
  python3 - "$output" "$city" <<'PY'
import json,sys
report=json.load(open(sys.argv[1],encoding="utf-8"))
events=report["source"]["occurrenceIds"]
if report.get("cityId") != sys.argv[2] or report["replica"]["occurrenceIds"] != events or not report.get("exactEventIds"):
    raise SystemExit("Bangkok event state is not exact")
for side in ("source","replica"):
    if not report[side].get("allSignaturesValid") or report[side].get("privateWrapperCount") != 0:
        raise SystemExit("Bangkok signature or privacy audit failed")
PY
}
capture_candidate_nip11() {
  output=$1 ip=$(candidate_ip)
  curl -fsS --max-time 5 -H 'Accept: application/nostr+json' "http://$ip:$port/" >"$output"
  python3 - "$output" <<'PY'
import json,re,sys
data=json.load(open(sys.argv[1],encoding="utf-8"))
if data.get("name") != "BitcoinWalk Bangkok relay" or data.get("version") != "bitcoinwalk-city-community-relay-0.9.15-rehearsal":
    raise SystemExit("unexpected combined-relay NIP-11 identity")
if not {1,11,29,42}.issubset(set(data.get("supported_nips",[]))):
    raise SystemExit("combined relay does not advertise the required NIPs")
if not re.fullmatch(r"[0-9a-f]{64}",data.get("self", "")):
    raise SystemExit("combined relay does not advertise its NIP-29 identity")
PY
}
verify_candidate_topology() {
  output=$1
  docker inspect "$candidate_container" >"$output"
  python3 - "$output" "$network" <<'PY'
import json,sys
container=json.load(open(sys.argv[1],encoding="utf-8"))[0]
if not container["State"]["Running"] or container["Config"]["User"] != "65532:65532" or not container["HostConfig"]["ReadonlyRootfs"]:
    raise SystemExit("candidate runtime identity or hardening changed")
if container["HostConfig"].get("PortBindings"):
    raise SystemExit("candidate unexpectedly publishes a host port")
if set(container["HostConfig"].get("Tmpfs",{})) != {"/tmp"}:
    raise SystemExit("candidate hardened tmpfs is unavailable")
if set(container["NetworkSettings"]["Networks"]) != {sys.argv[2]}:
    raise SystemExit("candidate Docker network changed")
PY
}

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp install cp chown chmod sync cmp grep; do command -v "$command" >/dev/null; done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
test -x "$artifact" && test -x "$audit" && test -x "$probe"
test -f "$live_state"
test "$(docker inspect "$live_container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$live_container" --format '{{.Config.Image}}')" = bitcoinwalk-city-relay:0.8.92
test -z "$(docker port "$live_container")"
for path in "$candidate_target" "$candidate_state"; do test ! -e "$path" || { echo "$path already exists; stop for review." >&2; exit 1; }; done
test -z "$(docker ps -aq --filter "name=^/${candidate_container}$")" || { echo "$candidate_container already exists; stop for review." >&2; exit 1; }
if docker image inspect "$candidate_image" >/dev/null 2>&1; then echo "$candidate_image already exists; stop for review." >&2; exit 1; fi
wait_live

backup=$(mktemp -d /var/backups/bitcoinwalk-bangkok-combined-rehearsal.XXXXXX)
chmod 0700 "$backup"
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' "$live_https" >"$backup/live-nip11.before.json"
audit_exact "$live_endpoint" "$backup/live-events.before.json"
docker inspect "$live_container" >"$backup/live-container.before.json"
completed=0
candidate_created=0
live_stopped=0
cleanup() {
  code=$?; trap - EXIT HUP INT TERM
  if test "$live_stopped" -eq 1; then docker start "$live_container" >/dev/null 2>&1 || true; fi
  remove_candidate
  if test "$completed" -ne 1; then echo "Bangkok combined-relay rehearsal did not complete; the live container was left on its accepted configuration. Evidence: $backup" >&2; fi
  exit "$code"
}
remove_candidate() {
  if test "$candidate_created" -eq 1; then
    test ! -f "$candidate_target/compose.yaml" || docker compose -p "$candidate_project" -f "$candidate_target/compose.yaml" down >/dev/null 2>&1 || true
    docker image rm "$candidate_image" >/dev/null 2>&1 || true
    rm -f "$candidate_target/bitcoinwalk-relay" "$candidate_target/Dockerfile" "$candidate_target/compose.yaml"
    rmdir "$candidate_target" 2>/dev/null || true
    rm -f "$candidate_state/events.db" "$candidate_state/chat.db" "$candidate_state/chat-relay-key"
    rmdir "$candidate_state" 2>/dev/null || true
  fi
}
trap cleanup EXIT HUP INT TERM

candidate_created=1
install -d -o root -g root -m 0755 "$candidate_target"
install -d -o 65532 -g 65532 -m 0700 "$candidate_state"
install -o root -g root -m 0555 "$artifact" "$candidate_target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile" "$candidate_target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$candidate_target/compose.yaml"

docker stop -t 20 "$live_container" >/dev/null
live_stopped=1
RELAY_REPLICA_DB_DIGEST="$live_state" "$artifact" >"$backup/live-event-digest.before"
cp -p "$live_state" "$backup/live-events.db"
cp -p "$live_state" "$candidate_state/events.db"
chown 65532:65532 "$candidate_state/events.db"
chmod 0600 "$candidate_state/events.db"
sync "$backup/live-events.db" "$candidate_state/events.db"
docker start "$live_container" >/dev/null
live_stopped=0
wait_live
audit_exact "$live_endpoint" "$backup/live-events.after-copy.json"
cmp "$backup/live-events.before.json" "$backup/live-events.after-copy.json"

RELAY_CHAT_KEY_INIT="$candidate_state/chat-relay-key" "$artifact"
chown 65532:65532 "$candidate_state/chat-relay-key"
chmod 0600 "$candidate_state/chat-relay-key"
cp -p "$candidate_state/chat-relay-key" "$backup/chat-relay-key.recovery"
docker build --pull=false --network=none -t "$candidate_image" "$candidate_target" >"$backup/docker-build.log"
docker compose -p "$candidate_project" -f "$candidate_target/compose.yaml" config >"$backup/compose.resolved.yaml"
docker compose -p "$candidate_project" -f "$candidate_target/compose.yaml" up -d --no-build >/dev/null
wait_candidate
verify_candidate_topology "$backup/candidate-container.first.json"
capture_candidate_nip11 "$backup/candidate-nip11.first.json"
ip=$(candidate_ip)
audit_exact "ws://$ip:$port" "$backup/candidate-events.first.json"
"$probe" -relay "ws://$ip:$port" -auth-relay "wss://bangkok.bitcoinwalk.org" >"$backup/candidate-chat.first.json"

docker stop -t 20 "$candidate_container" >/dev/null
RELAY_REPLICA_DB_DIGEST="$candidate_state/events.db" "$artifact" >"$backup/candidate-event-digest.first"
RELAY_REPLICA_DB_DIGEST="$candidate_state/chat.db" "$artifact" >"$backup/candidate-chat-digest.first"
cp -p "$candidate_state/chat.db" "$backup/candidate-chat.first.db"
docker start "$candidate_container" >/dev/null
wait_candidate
verify_candidate_topology "$backup/candidate-container.restart.json"
capture_candidate_nip11 "$backup/candidate-nip11.restart.json"
ip=$(candidate_ip)
audit_exact "ws://$ip:$port" "$backup/candidate-events.restart.json"
"$probe" -relay "ws://$ip:$port" -auth-relay "wss://bangkok.bitcoinwalk.org" >"$backup/candidate-chat.restart.json"
cmp "$backup/candidate-nip11.first.json" "$backup/candidate-nip11.restart.json"
cmp "$backup/candidate-events.first.json" "$backup/candidate-events.restart.json"
cmp "$backup/candidate-chat.first.json" "$backup/candidate-chat.restart.json"

docker stop -t 20 "$candidate_container" >/dev/null
RELAY_REPLICA_DB_DIGEST="$candidate_state/events.db" "$artifact" >"$backup/candidate-event-digest.restart"
RELAY_REPLICA_DB_DIGEST="$candidate_state/chat.db" "$artifact" >"$backup/candidate-chat-digest.restart"
cp -p "$candidate_state/chat.db" "$backup/candidate-chat.restart.db"
cmp "$backup/live-event-digest.before" "$backup/candidate-event-digest.first"
cmp "$backup/candidate-event-digest.first" "$backup/candidate-event-digest.restart"
cmp "$backup/candidate-chat-digest.first" "$backup/candidate-chat-digest.restart"

audit_exact "$live_endpoint" "$backup/live-events.accepted.json"
cmp "$backup/live-events.before.json" "$backup/live-events.accepted.json"
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' "$live_https" >"$backup/live-nip11.accepted.json"
cmp "$backup/live-nip11.before.json" "$backup/live-nip11.accepted.json"
test "$(docker inspect "$live_container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$live_container")"
docker logs "$candidate_container" >"$backup/candidate.log" 2>&1 || true
(
  cd "$backup"
  sha256sum live-events.db chat-relay-key.recovery live-event-digest.before candidate-event-digest.first candidate-event-digest.restart candidate-chat-digest.first candidate-chat-digest.restart candidate-chat.first.db candidate-chat.restart.db live-events.before.json live-events.after-copy.json live-events.accepted.json candidate-events.first.json candidate-events.restart.json candidate-chat.first.json candidate-chat.restart.json candidate-nip11.first.json candidate-nip11.restart.json live-nip11.before.json live-nip11.accepted.json live-container.before.json candidate-container.first.json candidate-container.restart.json compose.resolved.yaml docker-build.log candidate.log >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
remove_candidate
echo "Isolated Bangkok combined event/NIP-29 rehearsal accepted on 0.9.15. Evidence: $backup"
echo "The public Bangkok relay remained exact and unchanged; the candidate preserved its event digest and empty NIP-29 store across restart."
echo "Unauthorized group creation was denied, no host port was published and no DNS, proxy, registry or signed-directory state changed."
echo "Preserve the root-only rehearsed NIP-29 identity at $backup/chat-relay-key.recovery for activation."
