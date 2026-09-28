#!/bin/sh
# Restore the isolated candidate from its verified empty pre-backfill backup and
# upgrade it to replacement-aware receiver storage before replaying history.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-replica-candidate-pre-backfill.BACKUP" >&2; exit 1; }
cd "$(dirname "$0")/.."

restore=$1
case "$restore" in /var/backups/bitcoinwalk-replica-candidate-pre-backfill.*) ;; *) echo 'Refusing an unexpected restore path.' >&2;exit 1;; esac
test ! -L "$restore";test -d "$restore";test -f "$restore/events.db";test -f "$restore/SHA256SUMS"
(cd "$restore" && sha256sum -c SHA256SUMS)

manifest=REPLICA-CANDIDATE-REPLACEMENT-0.8.48-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-candidate-0.8.48
audit=./replica-audit-shadow-0.8.44
dockerfile=./deploy/Dockerfile.replica-candidate-0.8.48
compose_source=./deploy/compose.replica-candidate-0.8.48.yaml
target=/opt/bitcoinwalk-replica-candidate
state=/var/lib/bitcoinwalk-replica-candidate/events.db
project=bitcoinwalk-replica-candidate
container=bitcoinwalk-replica-candidate
old_image=bitcoinwalk-replica-candidate:0.8.47
image=bitcoinwalk-replica-candidate:0.8.48
network=root_my_custom_network
candidate=wss://replica.bitcoinwalk.org/
city=be8514a4-9df0-4159-a517-71f65761cbbe

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp install awk grep;do command -v "$command" >/dev/null;done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
docker image inspect "$old_image" >/dev/null
if docker image inspect "$image" >/dev/null 2>&1;then echo "$image already exists; stop for review." >&2;exit 1;fi
test -x "$target/bitcoinwalk-relay";test -f "$target/Dockerfile";test -f "$target/compose.yaml";test -f "$state"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
available_kb=$(df -Pk /var/lib/docker | awk 'NR==2 {print $4}')
test "$available_kb" -ge 524288 || { echo 'Less than 512 MiB is available; nothing changed.' >&2; exit 1; }

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-candidate-replacement.XXXXXX)
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
  docker stop -t 20 "$container" >/dev/null 2>&1||true
  install -o 65532 -g 65532 -m 0600 "$backup/events.dirty.db" "$state"
  install -o root -g root -m 0555 "$backup/bitcoinwalk-relay.before" "$target/bitcoinwalk-relay"
  install -o root -g root -m 0444 "$backup/Dockerfile.before" "$target/Dockerfile"
  install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target/compose.yaml"
  docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null 2>&1||true
 fi
 test "$image_built" -eq 0||docker image rm "$image" >/dev/null 2>&1||true
 if test "$completed" -ne 1;then echo "Candidate replacement restore did not complete; pre-restore state was restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

docker stop -t 20 "$container" >/dev/null
cp -p "$state" "$backup/events.dirty.db"
sync "$backup/events.dirty.db"
changed=1
install -o 65532 -g 65532 -m 0600 "$restore/events.db" "$state"
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile" "$target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$target/compose.yaml"
docker build --pull=false --network=none -t "$image" "$target"
image_built=1
test "$(docker run --rm --network none --read-only \
 -e RELAY_TIMEZONE_PROBE=America/Chicago "$image" 2>/dev/null)" = America/Chicago
docker compose -p "$project" -f "$target/compose.yaml" config >"$backup/compose.resolved.yaml"
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null

attempt=0
until curl --fail --silent --max-time 2 https://replica.bitcoinwalk.org/ >/dev/null;do
 attempt=$((attempt+1));test "$attempt" -lt 30||exit 1;sleep 1
done
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' https://replica.bitcoinwalk.org/ >"$backup/nip11.json"
grep -Eq '"version"[[:space:]]*:[[:space:]]*"bitcoinwalk-replica-candidate-0.8.48"' "$backup/nip11.json"
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
if set(container["HostConfig"].get("Tmpfs", {})) != {"/tmp"}:
    raise SystemExit("candidate validation tmpfs is unavailable")
PY
"$audit" -source "$candidate" -replica "$candidate" -city "$city" -allow-empty >"$backup/readback.after.json"
python3 - "$backup/readback.after.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if report["source"]["occurrenceIds"] or report["replica"]["occurrenceIds"]:
    raise SystemExit("restored candidate is not empty")
if not report["exactEventIds"] or not report["readOnly"]:
    raise SystemExit("restored candidate audit is not exact and read-only")
PY
test -z "$(docker port "$container")"
(
 cd "$backup"
 sha256sum bitcoinwalk-relay.before Dockerfile.before compose.before.yaml events.dirty.db container.before.json container.before.log readback.before.json readback.after.json compose.resolved.yaml nip11.json container.after.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Empty replacement-aware candidate accepted on 0.8.48. Backup: $backup"
echo "Restored database source: $restore"
echo 'The prior divergent database remains recoverable as events.dirty.db; the candidate is exact 0/0 and ready for one backfill retry.'
