#!/bin/sh
# Take a consistent post-backfill backup of the isolated Memphis candidate and
# prove exact public state survives start and a second explicit restart.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-CANDIDATE-POST-BACKFILL-0.8.52-SHA256SUMS
audit=./replica-audit-shadow-0.8.44
target=/opt/bitcoinwalk-replica-candidate
state=/var/lib/bitcoinwalk-replica-candidate/events.db
container=bitcoinwalk-replica-candidate
network=root_my_custom_network
city=be8514a4-9df0-4159-a517-71f65761cbbe
source=wss://relay-staging.bitcoinwalk.org/
existing=wss://replica-staging.bitcoinwalk.org/
candidate=wss://replica.bitcoinwalk.org/

audit_retry(){
 attempt=0
 until "$@";do
  attempt=$((attempt+1));test "$attempt" -lt 6||return 1
  echo 'Public audit has not converged; retrying in 5 seconds.' >&2
  sleep 5
 done
}
wait_candidate(){
 attempt=0
 until curl --fail --silent --max-time 2 https://replica.bitcoinwalk.org/ >/dev/null;do
  attempt=$((attempt+1));test "$attempt" -lt 30||return 1;sleep 1
 done
}
verify_report(){
 python3 - "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
expected = 7
if not report["readOnly"] or not report["exactEventIds"]:
    raise SystemExit("replica audit is not exact and read-only")
if len(report["source"]["occurrenceIds"]) != expected or len(report["replica"]["occurrenceIds"]) != expected:
    raise SystemExit("replica audit is not exact 7/7")
for side in ("source", "replica"):
    if not report[side]["allSignaturesValid"] or report[side]["privateWrapperCount"] != 0:
        raise SystemExit("replica signature or privacy acceptance failed")
PY
}
verify_topology(){
 docker inspect "$container" >"$1"
 python3 - "$1" "$network" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    container = json.load(stream)[0]
network = sys.argv[2]
if not container["State"]["Running"] or not container["HostConfig"]["ReadonlyRootfs"]:
    raise SystemExit("candidate is not running with a read-only root")
if container["HostConfig"].get("PortBindings"):
    raise SystemExit("candidate unexpectedly publishes a host port")
if set(container["HostConfig"].get("Tmpfs", {})) != {"/tmp"}:
    raise SystemExit("candidate hardened tmpfs is unavailable")
if set(container["NetworkSettings"]["Networks"]) != {network}:
    raise SystemExit("candidate Docker network changed")
PY
}

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp cp sync grep;do command -v "$command" >/dev/null;done
test -x "$target/bitcoinwalk-relay";test -f "$target/Dockerfile";test -f "$target/compose.yaml";test -f "$state"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' https://replica.bitcoinwalk.org/ | grep -Eq '"version"[[:space:]]*:[[:space:]]*"bitcoinwalk-replica-candidate-0.8.48"'

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-candidate-post-backfill.XXXXXX)
chmod 0700 "$backup"
completed=0
recover(){
 code=$?;trap - EXIT HUP INT TERM
 docker start "$container" >/dev/null 2>&1||true
 if test "$completed" -ne 1;then echo "Candidate post-backfill backup/restart rehearsal did not complete; the candidate was started for recovery. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

audit_retry "$audit" -source "$source" -replica "$candidate" -city "$city" >"$backup/candidate.before.json"
verify_report "$backup/candidate.before.json"
audit_retry "$audit" -source "$source" -replica "$existing" -city "$city" >"$backup/existing.before.json"
verify_report "$backup/existing.before.json"
verify_topology "$backup/container.before.json"
docker logs "$container" >"$backup/container.before.log" 2>&1

docker stop -t 20 "$container" >/dev/null
cp -p "$state" "$backup/events.db"
cp -p "$target/bitcoinwalk-relay" "$backup/bitcoinwalk-relay"
cp -p "$target/Dockerfile" "$backup/Dockerfile"
cp -p "$target/compose.yaml" "$backup/compose.yaml"
sync "$backup/events.db" "$backup/bitcoinwalk-relay" "$backup/Dockerfile" "$backup/compose.yaml"
echo "Consistent post-backfill candidate backup created: $backup"
docker start "$container" >/dev/null
wait_candidate

audit_retry "$audit" -source "$source" -replica "$candidate" -city "$city" >"$backup/candidate.after-start.json"
verify_report "$backup/candidate.after-start.json"
cmp "$backup/candidate.before.json" "$backup/candidate.after-start.json"
verify_topology "$backup/container.after-start.json"

docker restart -t 20 "$container" >/dev/null
wait_candidate
audit_retry "$audit" -source "$source" -replica "$candidate" -city "$city" >"$backup/candidate.after-restart.json"
verify_report "$backup/candidate.after-restart.json"
cmp "$backup/candidate.before.json" "$backup/candidate.after-restart.json"
audit_retry "$audit" -source "$source" -replica "$existing" -city "$city" >"$backup/existing.after.json"
verify_report "$backup/existing.after.json"
cmp "$backup/existing.before.json" "$backup/existing.after.json"
verify_topology "$backup/container.after-restart.json"
test -z "$(docker port "$container")"
(
 cd "$backup"
 sha256sum events.db bitcoinwalk-relay Dockerfile compose.yaml container.before.json container.before.log container.after-start.json container.after-restart.json candidate.before.json candidate.after-start.json candidate.after-restart.json existing.before.json existing.after.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Memphis candidate post-backfill backup and restart rehearsal accepted on 0.8.52. Backup: $backup"
echo 'Candidate remained exact 7/7 after start and repeated restart; the existing receiver remained exact 7/7.'
echo 'The candidate retained its read-only root, hardened tmpfs, private Docker network and zero host-published ports.'
echo 'No live registry, signed directory, DNS, Nginx Proxy Manager or source state was changed.'
