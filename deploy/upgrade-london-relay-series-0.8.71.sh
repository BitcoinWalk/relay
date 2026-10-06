#!/bin/sh
# Upgrade only the London receiver to accept a bounded eight-walk approval
# bundle. Preserve its database, private Docker topology and non-root runtime.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on .240.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=LONDON-RELAY-SERIES-0.8.71-SHA256SUMS
artifact=./bitcoinwalk-relay-london-series-0.8.71
audit=./replica-audit-london-series-0.8.71
dockerfile=./deploy/Dockerfile.london-relay-0.8.71
compose_source=./deploy/compose.london-relay-0.8.71.yaml
target=/opt/bitcoinwalk-relay-london
state=/var/lib/bitcoinwalk-relay-london/events.db
project=bitcoinwalk-relay-london
container=bitcoinwalk-relay-london
image=bitcoinwalk-relay-london:0.8.71
network=root_my_custom_network
city=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
endpoint=wss://london.bitcoinwalk.org/

wait_public() {
  attempt=0
  until curl --fail --silent --max-time 2 https://london.bitcoinwalk.org/ >/dev/null; do
    attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1
  done
}
verify_topology() {
  docker inspect "$container" >"$1"
  python3 - "$1" "$network" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    container = json.load(stream)[0]
if not container["State"]["Running"] or container["Config"]["User"] != "65532:65532" or not container["HostConfig"]["ReadonlyRootfs"]:
    raise SystemExit("London runtime identity or hardening changed")
if container["HostConfig"].get("PortBindings"):
    raise SystemExit("London receiver unexpectedly publishes a host port")
if set(container["HostConfig"].get("Tmpfs", {})) != {"/tmp"}:
    raise SystemExit("London hardened tmpfs is unavailable")
if set(container["NetworkSettings"]["Networks"]) != {sys.argv[2]}:
    raise SystemExit("London Docker network changed")
PY
}
audit_empty() {
  "$audit" -source "$endpoint" -replica "$endpoint" -city "$city" -allow-empty >"$1"
  python3 - "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if not report.get("exactEventIds"):
    raise SystemExit("London empty audit is not exact")
for side in ("source", "replica"):
    state = report[side]
    if state.get("occurrenceIds") != [] or not state.get("allSignaturesValid") or state.get("privateWrapperCount") != 0:
        raise SystemExit("London receiver changed before its visible-state seed")
PY
}

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp install cp sync grep cmp; do command -v "$command" >/dev/null; done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
test -x "$target/bitcoinwalk-relay" && test -f "$target/Dockerfile" && test -f "$target/compose.yaml" && test -f "$state"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
if docker image inspect "$image" >/dev/null 2>&1; then echo "$image already exists; stop for review." >&2; exit 1; fi
wait_public

backup=$(mktemp -d /var/backups/bitcoinwalk-london-relay-series.XXXXXX)
chmod 0700 "$backup"
cp -p "$target/bitcoinwalk-relay" "$backup/bitcoinwalk-relay.before"
cp -p "$target/Dockerfile" "$backup/Dockerfile.before"
cp -p "$target/compose.yaml" "$backup/compose.before.yaml"
docker inspect "$container" >"$backup/container.before.json"
docker logs "$container" >"$backup/container.before.log" 2>&1
audit_empty "$backup/readback.before.json"
completed=0
changed=0
image_built=0
recover() {
  code=$?; trap - EXIT HUP INT TERM
  if test "$changed" -eq 1; then
    install -o root -g root -m 0555 "$backup/bitcoinwalk-relay.before" "$target/bitcoinwalk-relay"
    install -o root -g root -m 0444 "$backup/Dockerfile.before" "$target/Dockerfile"
    install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target/compose.yaml"
    docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null 2>&1 || true
  fi
  test "$image_built" -eq 0 || docker image rm "$image" >/dev/null 2>&1 || true
  if test "$completed" -ne 1; then echo "London series-bundle upgrade did not complete; prior image and Compose configuration restored. Backup: $backup" >&2; fi
  exit "$code"
}
trap recover EXIT HUP INT TERM

docker stop -t 20 "$container" >/dev/null
cp -p "$state" "$backup/events.db"
sync "$backup/events.db"
changed=1
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile" "$target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$target/compose.yaml"
docker build --pull=false --network=none -t "$image" "$target"
image_built=1
docker compose -p "$project" -f "$target/compose.yaml" config >"$backup/compose.resolved.yaml"
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null
wait_public
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' https://london.bitcoinwalk.org/ >"$backup/nip11.json"
grep -Eq '"version"[[:space:]]*:[[:space:]]*"bitcoinwalk-city-relay-0.8.71"' "$backup/nip11.json"
audit_empty "$backup/readback.after.json"
cmp "$backup/readback.before.json" "$backup/readback.after.json"
verify_topology "$backup/container.after.json"
docker restart -t 20 "$container" >/dev/null
wait_public
audit_empty "$backup/readback.after-restart.json"
cmp "$backup/readback.before.json" "$backup/readback.after-restart.json"
test -z "$(docker port "$container")"
(
  cd "$backup"
  sha256sum bitcoinwalk-relay.before Dockerfile.before compose.before.yaml events.db container.before.json container.before.log readback.before.json readback.after.json readback.after-restart.json compose.resolved.yaml nip11.json container.after.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "London eight-walk bundle receiver accepted on 0.8.71. Backup: $backup"
echo 'The receiver remained empty across upgrade and restart with its non-root, private hardened topology unchanged.'
echo 'The bounded bundle policy now matches the signed maximum of eight initial city walks.'
