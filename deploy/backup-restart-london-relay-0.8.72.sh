#!/bin/sh
# Back up the seeded London receiver and prove exact public state survives
# repeated container restart without changing registry or directory state.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on .240.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=LONDON-RELAY-POST-SEED-0.8.72-SHA256SUMS
audit=./replica-audit-london-post-seed-0.8.72
container=bitcoinwalk-relay-london
state=/var/lib/bitcoinwalk-relay-london/events.db
network=root_my_custom_network
city=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
source=wss://relay-staging.bitcoinwalk.org/
london=wss://london.bitcoinwalk.org/

wait_public() {
  attempt=0
  until curl --fail --silent --max-time 2 https://london.bitcoinwalk.org/ >/dev/null; do
    attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1
  done
}
audit_exact() {
  output=$1
  attempt=0
  until "$audit" -source "$source" -replica "$london" -city "$city" >"$output" 2>/dev/null; do
    attempt=$((attempt+1)); test "$attempt" -lt 8 || return 1
    echo 'London post-seed audit has not converged; retrying in 5 seconds.' >&2
    sleep 5
  done
  python3 - "$output" "$city" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
expected = report["source"]["occurrenceIds"]
if report.get("cityId") != sys.argv[2] or len(expected) != 8 or report["replica"]["occurrenceIds"] != expected or not report.get("exactEventIds"):
    raise SystemExit("London receiver is not exact 8/8")
for side in ("source", "replica"):
    if not report[side].get("allSignaturesValid") or report[side].get("privateWrapperCount") != 0:
        raise SystemExit("London post-seed signature or privacy audit failed")
PY
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

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp cp sync grep cmp; do command -v "$command" >/dev/null; done
test -x "$audit" && test -f "$state"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
wait_public

backup=$(mktemp -d /var/backups/bitcoinwalk-london-relay-post-seed.XXXXXX)
chmod 0700 "$backup"
completed=0
recover() {
  code=$?; trap - EXIT HUP INT TERM
  docker start "$container" >/dev/null 2>&1 || true
  if test "$completed" -ne 1; then echo "London post-seed backup/restart acceptance did not complete. Backup: $backup" >&2; fi
  exit "$code"
}
trap recover EXIT HUP INT TERM

audit_exact "$backup/readback.before.json"
verify_topology "$backup/container.before.json"
docker stop -t 20 "$container" >/dev/null
cp -p "$state" "$backup/events.db"
sync "$backup/events.db"
docker start "$container" >/dev/null
wait_public
audit_exact "$backup/readback.after-start.json"
docker restart -t 20 "$container" >/dev/null
wait_public
audit_exact "$backup/readback.after-restart.json"
cmp "$backup/readback.before.json" "$backup/readback.after-start.json"
cmp "$backup/readback.before.json" "$backup/readback.after-restart.json"
verify_topology "$backup/container.after.json"
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' https://london.bitcoinwalk.org/ >"$backup/nip11.json"
grep -Eq '"version"[[:space:]]*:[[:space:]]*"bitcoinwalk-city-relay-0.8.71"' "$backup/nip11.json"
docker logs "$container" >"$backup/container.log" 2>&1
test -z "$(docker port "$container")"
(
  cd "$backup"
  sha256sum events.db readback.before.json readback.after-start.json readback.after-restart.json container.before.json container.after.json nip11.json container.log >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "London post-seed backup and restart accepted on 0.8.72. Backup: $backup"
echo 'The dedicated receiver remained exact 8/8 after start and repeated restart.'
echo 'Its non-root, read-only, hardened private-network topology remained unchanged.'
echo 'No source registry, journal, signed directory, DNS or proxy configuration was changed.'
