#!/bin/sh
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-MODERATION-0.8.63-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-moderation-0.8.63
audit=./replica-audit-shadow-0.8.44
dockerfile=./deploy/Dockerfile.replica-moderation-0.8.63
compose_source=./deploy/compose.replica-candidate-0.8.63.yaml
target=/opt/bitcoinwalk-replica-candidate
state=/var/lib/bitcoinwalk-replica-candidate/events.db
container=bitcoinwalk-replica-candidate
project=bitcoinwalk-replica-candidate
network=root_my_custom_network
image=bitcoinwalk-replica-candidate:0.8.63
city=be8514a4-9df0-4159-a517-71f65761cbbe
source=wss://relay-staging.bitcoinwalk.org/
existing=wss://replica-staging.bitcoinwalk.org/
candidate=wss://replica.bitcoinwalk.org/

wait_candidate(){
 attempt=0
 until curl --fail --silent --max-time 2 https://replica.bitcoinwalk.org/ >/dev/null;do
  attempt=$((attempt+1));test "$attempt" -lt 30||return 1;sleep 1
 done
}
audit_retry(){
 attempt=0
 until "$@";do
  attempt=$((attempt+1));test "$attempt" -lt 6||return 1
  echo 'Public audit has not converged; retrying in 5 seconds.' >&2;sleep 5
 done
}
verify_report(){
 python3 - "$1" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if not report["readOnly"] or not report["exactEventIds"]:
    raise SystemExit("replica audit is not exact and read-only")
if len(report["source"]["occurrenceIds"]) != 7 or len(report["replica"]["occurrenceIds"]) != 7:
    raise SystemExit("replica audit is not exact 7/7")
for side in ("source", "replica"):
    if not report[side]["allSignaturesValid"] or report[side]["privateWrapperCount"] != 0:
        raise SystemExit("replica signature or privacy acceptance failed")
PY
}
verify_topology(){
 docker inspect "$container" >"$1"
 python3 - "$1" "$network" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    container = json.load(stream)[0]
if not container["State"]["Running"] or not container["HostConfig"]["ReadonlyRootfs"]:
    raise SystemExit("candidate runtime hardening changed")
if container["HostConfig"].get("PortBindings"):
    raise SystemExit("candidate unexpectedly publishes a host port")
if set(container["HostConfig"].get("Tmpfs", {})) != {"/tmp"}:
    raise SystemExit("candidate hardened tmpfs is unavailable")
if set(container["NetworkSettings"]["Networks"]) != {sys.argv[2]}:
    raise SystemExit("candidate Docker network changed")
PY
}

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp install cp sync grep;do command -v "$command" >/dev/null;done
docker compose version >/dev/null;docker network inspect "$network" >/dev/null
test -x "$artifact";test -x "$audit";test -f "$state"
test -x "$target/bitcoinwalk-relay";test -f "$target/Dockerfile";test -f "$target/compose.yaml"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
audit_retry "$audit" -source "$source" -replica "$candidate" -city "$city" > /tmp/bitcoinwalk-candidate-0.8.63.before.json
verify_report /tmp/bitcoinwalk-candidate-0.8.63.before.json

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-moderation-candidate.XXXXXX);chmod 0700 "$backup"
cp -p "$state" "$backup/events.db";cp -p "$target/bitcoinwalk-relay" "$backup/bitcoinwalk-relay.before"
cp -p "$target/Dockerfile" "$backup/Dockerfile.before";cp -p "$target/compose.yaml" "$backup/compose.before.yaml"
cp -p /tmp/bitcoinwalk-candidate-0.8.63.before.json "$backup/candidate.before.json"
docker inspect "$container" >"$backup/container.before.json";docker logs "$container" >"$backup/container.before.log" 2>&1
sync "$backup/events.db";echo "Consistent pre-upgrade candidate backup created: $backup"

install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile" "$target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$target/compose.yaml"
docker build --pull=false --network=none -t "$image" "$target"
docker compose -p "$project" -f "$target/compose.yaml" config >"$backup/compose.resolved.yaml"
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null
wait_candidate
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' https://replica.bitcoinwalk.org/ >"$backup/nip11.json"
grep -Eq '"version"[[:space:]]*:[[:space:]]*"bitcoinwalk-replica-candidate-0.8.63"' "$backup/nip11.json"
audit_retry "$audit" -source "$source" -replica "$candidate" -city "$city" >"$backup/candidate.after.json";verify_report "$backup/candidate.after.json"
audit_retry "$audit" -source "$source" -replica "$existing" -city "$city" >"$backup/existing.after.json";verify_report "$backup/existing.after.json"
verify_topology "$backup/container.after.json"
docker restart -t 20 "$container" >/dev/null;wait_candidate
audit_retry "$audit" -source "$source" -replica "$candidate" -city "$city" >"$backup/candidate.after-restart.json";verify_report "$backup/candidate.after-restart.json"
cmp "$backup/candidate.before.json" "$backup/candidate.after.json";cmp "$backup/candidate.before.json" "$backup/candidate.after-restart.json"
test -z "$(docker port "$container")"
(cd "$backup" && find . -maxdepth 1 -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
echo "Memphis candidate moderation receiver 0.8.63 accepted. Backup: $backup"
echo 'The candidate remained exact 7/7 across upgrade and restart with its private hardened Docker topology unchanged.'
