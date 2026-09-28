#!/bin/sh
# Resume the candidate tmpfs activation with Docker's authoritative
# HostConfig.Tmpfs representation. Docker does not include tmpfs entries in the
# generic Mounts array on this host.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-CANDIDATE-TMPFS-0.8.46-SHA256SUMS
compose_source=./deploy/compose.replica-candidate-0.8.45.yaml
audit=./replica-audit-shadow-0.8.44
target=/opt/bitcoinwalk-replica-candidate/compose.yaml
state=/var/lib/bitcoinwalk-replica-candidate/events.db
project=bitcoinwalk-replica-candidate
container=bitcoinwalk-replica-candidate
image=bitcoinwalk-replica-candidate:0.8.43
network=root_my_custom_network
candidate=wss://replica.bitcoinwalk.org/
city=be8514a4-9df0-4159-a517-71f65761cbbe

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp install;do command -v "$command" >/dev/null;done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
docker image inspect "$image" >/dev/null
test -f "$target";test -f "$state"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-candidate-tmpfs-resume.XXXXXX)
chmod 0700 "$backup"
cp -p "$target" "$backup/compose.before.yaml"
docker inspect "$container" >"$backup/container.before.json"
docker logs "$container" >"$backup/container.before.log" 2>&1
completed=0
changed=0
recover(){
 code=$?;trap - EXIT HUP INT TERM
 if test "$changed" -eq 1;then
  install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target"
  docker compose -p "$project" -f "$target" up -d --no-build --force-recreate >/dev/null 2>&1||true
 fi
 if test "$completed" -ne 1;then echo "Candidate tmpfs resume did not complete; prior Compose configuration restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

docker stop -t 20 "$container" >/dev/null
cp -p "$state" "$backup/events.db"
sync "$backup/events.db"
changed=1
install -o root -g root -m 0444 "$compose_source" "$target"
docker compose -p "$project" -f "$target" config >"$backup/compose.resolved.yaml"
docker compose -p "$project" -f "$target" up -d --no-build --force-recreate >/dev/null

attempt=0
until curl --fail --silent --max-time 2 https://replica.bitcoinwalk.org/ >/dev/null;do
 attempt=$((attempt+1));test "$attempt" -lt 30||exit 1;sleep 1
done
docker inspect "$container" >"$backup/container.after.json"
python3 - "$backup/container.after.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    container = json.load(stream)[0]
if not container["State"]["Running"]:
    raise SystemExit("candidate is not running")
if not container["HostConfig"]["ReadonlyRootfs"]:
    raise SystemExit("candidate root filesystem is no longer read-only")
if container["HostConfig"]["PortBindings"]:
    raise SystemExit("candidate unexpectedly publishes a host port")
tmpfs = container["HostConfig"].get("Tmpfs", {})
if set(tmpfs) != {"/tmp"}:
    raise SystemExit("candidate has an unexpected tmpfs configuration")
options = set(tmpfs["/tmp"].split(","))
for required in {"rw", "noexec", "nosuid", "nodev", "uid=65532", "gid=65532"}:
    if required not in options:
        raise SystemExit(f"candidate tmpfs is missing {required}")
if not options.intersection({"size=32m", "size=33554432"}):
    raise SystemExit("candidate tmpfs has the wrong size")
if not options.intersection({"mode=0700", "mode=700"}):
    raise SystemExit("candidate tmpfs has the wrong mode")
PY
"$audit" -source "$candidate" -replica "$candidate" -city "$city" -allow-empty >"$backup/readback.json"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
(
 cd "$backup"
 sha256sum compose.before.yaml compose.resolved.yaml events.db container.before.json container.before.log container.after.json readback.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Candidate validation tmpfs resume accepted on 0.8.46. Backup: $backup"
echo 'Docker HostConfig confirms the bounded hardened tmpfs; the root remains read-only and no host port is published.'
echo 'The candidate public event set was read back without mutation.'
