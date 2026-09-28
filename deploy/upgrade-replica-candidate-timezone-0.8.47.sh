#!/bin/sh
# Upgrade only the isolated candidate receiver to a binary with Go's timezone
# database embedded. This keeps the scratch image, hardened tmpfs and private
# Docker topology while enabling IANA timezone validation.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-CANDIDATE-TIMEZONE-0.8.47-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-candidate-0.8.47
audit=./replica-audit-shadow-0.8.44
dockerfile=./deploy/Dockerfile.replica-candidate-0.8.47
compose_source=./deploy/compose.replica-candidate-0.8.47.yaml
target=/opt/bitcoinwalk-replica-candidate
state=/var/lib/bitcoinwalk-replica-candidate/events.db
project=bitcoinwalk-replica-candidate
container=bitcoinwalk-replica-candidate
old_image=bitcoinwalk-replica-candidate:0.8.43
image=bitcoinwalk-replica-candidate:0.8.47
network=root_my_custom_network
candidate=wss://replica.bitcoinwalk.org/
city=be8514a4-9df0-4159-a517-71f65761cbbe

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp install cmp awk grep;do command -v "$command" >/dev/null;done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
docker image inspect "$old_image" >/dev/null
if docker image inspect "$image" >/dev/null 2>&1;then echo "$image already exists; stop for review." >&2;exit 1;fi
test -x "$target/bitcoinwalk-relay";test -f "$target/Dockerfile";test -f "$target/compose.yaml";test -f "$state"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
available_kb=$(df -Pk /var/lib/docker | awk 'NR==2 {print $4}')
test "$available_kb" -ge 524288 || { echo 'Less than 512 MiB is available; nothing changed.' >&2; exit 1; }

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-candidate-timezone.XXXXXX)
chmod 0700 "$backup"
cp -p "$target/bitcoinwalk-relay" "$backup/bitcoinwalk-relay.before"
cp -p "$target/Dockerfile" "$backup/Dockerfile.before"
cp -p "$target/compose.yaml" "$backup/compose.before.yaml"
docker inspect "$container" >"$backup/container.before.json"
docker logs "$container" >"$backup/container.before.log" 2>&1
"$audit" -source "$candidate" -replica "$candidate" -city "$city" -allow-empty >"$backup/readback.before.json"
completed=0
changed=0
image_built=0
recover(){
 code=$?;trap - EXIT HUP INT TERM
 if test "$changed" -eq 1;then
  install -o root -g root -m 0555 "$backup/bitcoinwalk-relay.before" "$target/bitcoinwalk-relay"
  install -o root -g root -m 0444 "$backup/Dockerfile.before" "$target/Dockerfile"
  install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target/compose.yaml"
  docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null 2>&1||true
 fi
 test "$image_built" -eq 0||docker image rm "$image" >/dev/null 2>&1||true
 if test "$completed" -ne 1;then echo "Candidate timezone upgrade did not complete; prior image and Compose configuration restored. Backup: $backup" >&2;fi
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

test "$(docker run --rm --network none --read-only \
 -e RELAY_TIMEZONE_PROBE=America/Chicago "$image" 2>/dev/null)" = America/Chicago
if docker run --rm --network none --read-only \
 -e RELAY_TIMEZONE_PROBE=Not/A_Real_Zone "$image" >/dev/null 2>&1;then
 echo 'Scratch-image timezone probe accepted an invalid zone.' >&2
 exit 1
fi

docker compose -p "$project" -f "$target/compose.yaml" config >"$backup/compose.resolved.yaml"
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null
attempt=0
until curl --fail --silent --max-time 2 https://replica.bitcoinwalk.org/ >/dev/null;do
 attempt=$((attempt+1));test "$attempt" -lt 30||exit 1;sleep 1
done
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' https://replica.bitcoinwalk.org/ >"$backup/nip11.json"
grep -Eq '"version"[[:space:]]*:[[:space:]]*"bitcoinwalk-replica-candidate-0.8.47"' "$backup/nip11.json"
docker inspect "$container" >"$backup/container.after.json"
python3 - "$backup/container.after.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    container = json.load(stream)[0]
if not container["State"]["Running"] or not container["HostConfig"]["ReadonlyRootfs"]:
    raise SystemExit("candidate runtime hardening changed")
if container["HostConfig"]["PortBindings"]:
    raise SystemExit("candidate unexpectedly publishes a host port")
tmpfs = container["HostConfig"].get("Tmpfs", {})
if set(tmpfs) != {"/tmp"}:
    raise SystemExit("candidate validation tmpfs is unavailable")
PY
"$audit" -source "$candidate" -replica "$candidate" -city "$city" -allow-empty >"$backup/readback.after.json"
cmp "$backup/readback.before.json" "$backup/readback.after.json"
test -z "$(docker port "$container")"
(
 cd "$backup"
 sha256sum bitcoinwalk-relay.before Dockerfile.before compose.before.yaml events.db container.before.json container.before.log readback.before.json readback.after.json compose.resolved.yaml nip11.json container.after.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Candidate embedded-timezone upgrade accepted on 0.8.47. Backup: $backup"
echo 'America/Chicago passed and an invalid zone failed inside the scratch image.'
echo 'The candidate public event set, read-only root, hardened tmpfs and private-network-only topology were preserved.'
