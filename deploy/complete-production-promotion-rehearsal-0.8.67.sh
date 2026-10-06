#!/bin/sh
# Complete acceptance from a 0.8.64 rehearsal that reached every relay audit
# but encountered a transient final application health probe.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-production-promotion-rehearsal.BACKUP" >&2; exit 1; }
evidence=$1
case "$evidence" in /var/backups/bitcoinwalk-production-promotion-rehearsal.*) ;; *) echo 'Refusing an unexpected evidence path.' >&2; exit 1;; esac
test -d "$evidence" && test ! -L "$evidence"
cd "$(dirname "$0")/.."

sha256sum -c PRODUCTION-PROMOTION-ACCEPTANCE-0.8.67-SHA256SUMS
for file in staging-source.db production-live.db production.candidate.db promotion-result.json app-health.before.json cities.tsv nip11-before-restart.json nip11-after-restart.json; do
  test -f "$evidence/$file" && test ! -L "$evidence/$file"
done
# The 0.8.64 reader opened staging-source.db through BoltDB and therefore
# changed only that disposable snapshot's bookkeeping pages. The production
# backup was never opened and must retain its original byte checksum.
test "$(grep -c '/production-live.db$' "$evidence/SHA256SUMS")" -eq 1
grep '/production-live.db$' "$evidence/SHA256SUMS" | sha256sum -c -
cmp "$evidence/nip11-before-restart.json" "$evidence/nip11-after-restart.json"

RELAY_REPLICA_DB_DIGEST="$evidence/staging-source.db" ./bitcoinwalk-relay-production-promotion-0.8.64 >"$evidence/staging-source.logical-digest"
RELAY_REPLICA_DB_DIGEST="$evidence/production.candidate.db" ./bitcoinwalk-relay-production-promotion-0.8.64 >"$evidence/production-candidate.logical-digest"
grep -Eq '^[0-9a-f]{64}$' "$evidence/staging-source.logical-digest"
grep -Eq '^[0-9a-f]{64}$' "$evidence/production-candidate.logical-digest"

python3 - "$evidence" deploy/production-promotion-0.8.64.json <<'PY'
import json
import pathlib
import sys

evidence = pathlib.Path(sys.argv[1])
manifest_path = pathlib.Path(sys.argv[2])
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
result = json.loads((evidence / "promotion-result.json").read_text(encoding="utf-8"))
cities = manifest["cities"]
paid = sorted(city["cityId"] for city in cities if city["tier"] == "paid")
free = sorted(city["cityId"] for city in cities if city["tier"] == "free")
expected_paid = ["ca2f9905-fb4d-4948-a12c-c792b28ec7c8"]
if paid != expected_paid or result.get("paidCities") != expected_paid:
    raise SystemExit("London is not the sole paid city")
if result.get("freeCities") != free or len(free) != 11:
    raise SystemExit("free-city promotion set changed")
if result.get("eventCount", 0) <= 0 or not result.get("eventDigest"):
    raise SystemExit("promotion result is incomplete")
for city in cities:
    slug = city["slug"]
    before = evidence / f"audit-{slug}.json"
    after = evidence / f"audit-after-{slug}.json"
    if not before.is_file() or not after.is_file() or before.read_bytes() != after.read_bytes():
        raise SystemExit(f"restart audit mismatch for {slug}")
    audit = json.loads(before.read_text(encoding="utf-8"))
    if not audit.get("exactEventIds"):
        raise SystemExit(f"event IDs are not exact for {slug}")
    for side in ("source", "replica"):
        if not audit.get(side, {}).get("allSignaturesValid"):
            raise SystemExit(f"invalid signature reported for {slug} {side}")
        if audit.get(side, {}).get("privateWrapperCount") != 0:
            raise SystemExit(f"private wrapper leaked for {slug} {side}")
print(f"Validated {len(cities)} cities, {result['eventCount']} signed events and London as the sole paid city.")
PY

production_service=bitcoinwalk-relay-production.service
production_current=$(mktemp "$evidence/production-current.XXXXXX")
production_current_name=$(basename "$production_current")
production_stopped=0
recover_production() {
  code=$?
  trap - EXIT HUP INT TERM
  if test "$production_stopped" -eq 1; then systemctl start "$production_service" >/dev/null 2>&1 || true; fi
  exit "$code"
}
trap recover_production EXIT HUP INT TERM
systemctl stop "$production_service"
production_stopped=1
cp -p /var/lib/bitcoinwalk-relay-production/events.db "$production_current"
systemctl start "$production_service"
production_stopped=0
attempt=0
until curl --fail --silent --max-time 5 http://127.0.0.1:3340/healthz >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 30 || { echo 'Production relay health did not recover.' >&2; exit 1; }; sleep 1
done
trap - EXIT HUP INT TERM
RELAY_REPLICA_DB_DIGEST="$evidence/production-live.db" ./bitcoinwalk-relay-production-promotion-0.8.64 >"$evidence/production-live.logical-digest"
RELAY_REPLICA_DB_DIGEST="$production_current" ./bitcoinwalk-relay-production-promotion-0.8.64 >"$evidence/production-current.logical-digest"
grep -Eq '^[0-9a-f]{64}$' "$evidence/production-live.logical-digest"
grep -Eq '^[0-9a-f]{64}$' "$evidence/production-current.logical-digest"
cmp "$evidence/production-live.logical-digest" "$evidence/production-current.logical-digest"
for unit in bitcoinwalk-relay.service bitcoinwalk-relay-production.service caddy.service; do systemctl is-active --quiet "$unit"; done

health="$evidence/app-health.accepted.json"
attempt=0
until curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz >"$health"; do
  attempt=$((attempt+1)); test "$attempt" -lt 30 || { echo 'Application health did not recover.' >&2; exit 1; }; sleep 1
done
python3 - "$evidence/app-health.before.json" "$health" <<'PY'
import json
import sys
before = json.load(open(sys.argv[1], encoding="utf-8"))
after = json.load(open(sys.argv[2], encoding="utf-8"))
for key in ("status", "app", "release"):
    if before.get(key) != after.get(key):
        raise SystemExit(f"application health field changed: {key}")
if after.get("status") != "ok":
    raise SystemExit("application is not healthy")
PY

(
  cd "$evidence"
  sha256sum staging-source.db production-live.db "$production_current_name" production.candidate.db staging-source.logical-digest production-candidate.logical-digest production-live.logical-digest production-current.logical-digest promotion-result.json cities.tsv app-health.before.json app-health.accepted.json nip11-before-restart.json nip11-after-restart.json candidate-start-1.log candidate-start-2.log audit-*.json >SHA256SUMS.accepted
)
chmod 0600 "$evidence/SHA256SUMS.accepted" "$health"
echo "Selective production promotion rehearsal accepted on 0.8.67. Evidence: $evidence"
echo 'All 12 approved cities matched across restart; London is the sole paid city and 11 cities remain free.'
echo 'The live production relay retained the same stable logical database digest; no DNS, Caddy, app, directory or live relay database was changed.'
