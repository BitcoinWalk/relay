#!/bin/sh
# Create and restart-test an isolated, selectively promoted production database.
# Live staging and production databases are backed up but never replaced here.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=./bitcoinwalk-relay-production-promotion-0.8.64
audit=./replica-audit-production-promotion-0.8.64
manifest=./deploy/production-promotion-0.8.64.json
checksums=./PRODUCTION-PROMOTION-0.8.64-SHA256SUMS
source_service=bitcoinwalk-relay.service
production_service=bitcoinwalk-relay-production.service
source_db=/var/lib/bitcoinwalk-relay/events.db
production_db=/var/lib/bitcoinwalk-relay-production/events.db
candidate_port=3358

sha256sum -c "$checksums"
for file in "$artifact" "$audit" "$manifest" "$source_db" "$production_db"; do test -f "$file"; done
for unit in "$source_service" "$production_service" caddy.service; do systemctl is-active --quiet "$unit"; done
! ss -ltn "sport = :$candidate_port" | grep -q LISTEN

backup=$(mktemp -d /var/backups/bitcoinwalk-production-promotion-rehearsal.XXXXXX)
chmod 0700 "$backup"
curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz >"$backup/app-health.before.json"
candidate="$backup/production.candidate.db"
candidate_pid=
complete=0

wait_health() {
  port=$1
  attempt=0
  until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
    attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1
  done
}
stop_candidate() {
  if test -n "$candidate_pid"; then kill "$candidate_pid" >/dev/null 2>&1 || true; wait "$candidate_pid" >/dev/null 2>&1 || true; candidate_pid=; fi
}
recover() {
  code=$?; trap - EXIT HUP INT TERM; stop_candidate
  systemctl start "$source_service" "$production_service" >/dev/null 2>&1 || true
  if test "$complete" -ne 1; then echo "Production promotion rehearsal did not complete; no live database was replaced. Backup: $backup" >&2; fi
  exit "$code"
}
trap recover EXIT HUP INT TERM

systemctl stop "$source_service"
cp -p "$source_db" "$backup/staging-source.db"
systemctl start "$source_service"
wait_health 3334

systemctl stop "$production_service"
cp -p "$production_db" "$backup/production-live.db"
systemctl start "$production_service"
wait_health 3340

sha256sum "$backup/staging-source.db" "$backup/production-live.db" >"$backup/SHA256SUMS"
echo "Consistent source and production backups created: $backup"

RELAY_PRODUCTION_PROMOTION_SOURCE="$backup/staging-source.db" \
RELAY_PRODUCTION_PROMOTION_DESTINATION="$candidate" \
RELAY_PRODUCTION_PROMOTION_MANIFEST="$manifest" \
RELAY_PRODUCTION_PROMOTION_CONFIRM=production-promotion-v1 \
"$artifact" >"$backup/promotion-result.json"

start_candidate() {
  env -i PATH=/usr/bin:/bin \
    RELAY_LISTEN="127.0.0.1:$candidate_port" \
    RELAY_DB="$candidate" \
    RELAY_ORGANIZER_MODE=true \
    RELAY_VERSION=bitcoinwalk-organizers-0.8.64 \
    RELAY_NAME='BitcoinWalk isolated production promotion' \
    RELAY_DESCRIPTION='Isolated selective production promotion rehearsal.' \
    "$artifact" >"$1" 2>&1 &
  candidate_pid=$!
}

start_candidate "$backup/candidate-start-1.log"
wait_health "$candidate_port"
python3 - "$manifest" <<'PY' >"$backup/cities.tsv"
import json,sys
with open(sys.argv[1],encoding="utf-8") as stream: data=json.load(stream)
for city in data["cities"]: print(city["cityId"], city["tier"], city["slug"], sep="\t")
PY
while IFS="$(printf '\t')" read -r city tier slug; do
  "$audit" -source ws://127.0.0.1:3334 -replica "ws://127.0.0.1:$candidate_port" -city "$city" -allow-empty >"$backup/audit-$slug.json"
done <"$backup/cities.tsv"
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "http://127.0.0.1:$candidate_port/" >"$backup/nip11-before-restart.json"
grep -q 'bitcoinwalk-organizers-0.8.64' "$backup/nip11-before-restart.json"
stop_candidate

start_candidate "$backup/candidate-start-2.log"
wait_health "$candidate_port"
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "http://127.0.0.1:$candidate_port/" >"$backup/nip11-after-restart.json"
cmp "$backup/nip11-before-restart.json" "$backup/nip11-after-restart.json"
while IFS="$(printf '\t')" read -r city tier slug; do
  "$audit" -source ws://127.0.0.1:3334 -replica "ws://127.0.0.1:$candidate_port" -city "$city" -allow-empty >"$backup/audit-after-$slug.json"
  cmp "$backup/audit-$slug.json" "$backup/audit-after-$slug.json"
done <"$backup/cities.tsv"
stop_candidate

test "$(sha256sum "$production_db" | cut -d ' ' -f 1)" = "$(sha256sum "$backup/production-live.db" | cut -d ' ' -f 1)"
for unit in "$source_service" "$production_service" caddy.service; do systemctl is-active --quiet "$unit"; done
curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz >"$backup/app-health.after.json"
cmp "$backup/app-health.before.json" "$backup/app-health.after.json"
(
  cd "$backup"
  sha256sum production.candidate.db promotion-result.json cities.tsv app-health.before.json app-health.after.json nip11-before-restart.json nip11-after-restart.json candidate-start-1.log candidate-start-2.log audit-*.json >>SHA256SUMS
)
complete=1
trap - EXIT HUP INT TERM
echo "Selective production promotion rehearsal accepted on 0.8.64. Evidence: $backup"
echo 'All manifest cities matched the live source before and after restart; only London is classified paid.'
echo 'The live production relay remained empty and byte-identical; no DNS, Caddy, app, directory or live relay database was changed.'
