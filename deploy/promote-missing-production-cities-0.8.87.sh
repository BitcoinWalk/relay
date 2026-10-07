#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"

checksums=PRODUCTION-ADDITIVE-CITIES-0.8.87-SHA256SUMS
builder="$root_dir/bitcoinwalk-relay-production-additive-0.8.85"
audit="$root_dir/replica-audit-production-additive-0.8.85"
additions="$root_dir/deploy/production-additive-cities-0.8.85.json"
existing="$root_dir/deploy/production-promotion-0.8.64.json"
source_service=bitcoinwalk-relay.service
production_service=bitcoinwalk-relay-production.service
source_db=/var/lib/bitcoinwalk-relay/events.db
production_db=/var/lib/bitcoinwalk-relay-production/events.db
production_binary=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
registry=/etc/bitcoinwalk-replication-production/registry.json
ledger=/etc/bitcoinwalk-replication-production/entitlements.json
journal=/var/lib/bitcoinwalk-relay-production/replication-journal.db
london=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
london_relay=wss://london.bitcoinwalk.org/
port=3362

wait_health(){
 local url=$1 ready=false
 for attempt in $(seq 1 40);do
  if curl --fail --silent --max-time 3 "$url" >/dev/null;then ready=true;break;fi
  sleep 1
 done
 "$ready"
}
verify_audit(){
 python3 - "$1" "$2" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: report=json.load(stream)
if report.get("cityId") != sys.argv[2] or not report.get("readOnly") or not report.get("exactEventIds"):
    raise SystemExit("city audit is not exact and read-only")
if report["source"]["occurrenceIds"] != report["replica"]["occurrenceIds"]:
    raise SystemExit("city occurrence IDs differ")
for side in ("source", "replica"):
    if not report[side]["allSignaturesValid"] or report[side]["privateWrapperCount"] != 0:
        raise SystemExit("city audit failed signature or privacy acceptance")
PY
}
city_ids(){
 python3 - "$existing" "$additions" <<'PY'
import json, sys
seen=set()
for path in sys.argv[1:]:
    with open(path, encoding="utf-8") as stream: manifest=json.load(stream)
    for city in manifest["cities"]:
        if city["cityId"] not in seen:
            seen.add(city["cityId"]);print(city["cityId"])
PY
}
audit_all(){
 local replica=$1 prefix=$2
 city_ids | while read -r city;do
  report="$prefix.$city.json"
  "$audit" -source ws://127.0.0.1:3334 -replica "$replica" -city "$city" -allow-empty >"$report"
  verify_audit "$report" "$city"
 done
}

sha256sum -c "$checksums"
for command in curl python3 sha256sum mktemp install systemctl stat grep cmp readlink;do command -v "$command" >/dev/null;done
for file in "$builder" "$audit" "$additions" "$existing" "$source_db" "$production_db" "$production_binary" "$registry" "$ledger" "$journal";do test -f "$file" && test ! -L "$file";done
test -x "$builder";test -x "$audit";test -x "$production_binary"
for unit in "$source_service" "$production_service" bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3334/healthz >/dev/null
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3340/healthz >/dev/null
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3345/api/healthz | grep -q '"release":"app-production-0.3.191"'
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ | grep -q 'bitcoinwalk-production-london-0.8.78'
systemctl cat "$production_service" | grep -q 'RELAY_REPLICA_REGISTRY=/etc/bitcoinwalk-replication-production/registry.json'

backup=$(mktemp -d /var/backups/bitcoinwalk-production-additive-cities.XXXXXX);chmod 0700 "$backup"
isolated="$backup/isolated";mkdir "$isolated";chmod 0700 "$isolated"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3345/api/healthz >"$backup/app-health.before.json"
cp -p "$registry" "$backup/registry.before.json"
cp -p "$ledger" "$backup/entitlements.before.json"

isolated_pid=;activated=false;completed=false
stop_isolated(){
 if test -n "$isolated_pid";then kill "$isolated_pid" >/dev/null 2>&1||true;wait "$isolated_pid" >/dev/null 2>&1||true;isolated_pid=;fi
}
recover(){
 code=$?;trap - EXIT HUP INT TERM;stop_isolated
 systemctl reset-failed "$source_service" >/dev/null 2>&1||true
 systemctl start "$source_service" >/dev/null 2>&1||true
 if "$activated";then
  systemctl stop "$production_service" >/dev/null 2>&1||true
  install -o "$production_uid" -g "$production_gid" -m 0600 "$backup/production-events.before.db" "$production_db"
 fi
 systemctl reset-failed "$production_service" >/dev/null 2>&1||true
 systemctl start "$production_service" >/dev/null 2>&1||true
 if ! "$completed";then echo "Missing-city production promotion did not complete; prior production database restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

systemctl stop "$source_service"
cp -p "$source_db" "$backup/staging-source.db"
systemctl start "$source_service";wait_health http://127.0.0.1:3334/healthz

production_uid=$(stat -Lc %u "$production_db");production_gid=$(stat -Lc %g "$production_db")
systemctl stop "$production_service"
cp -p "$production_db" "$backup/production-events.before.db"
cp -p "$journal" "$backup/production-journal.before.db"
systemctl start "$production_service";wait_health http://127.0.0.1:3340/healthz
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/production-journal.before.db" "$builder" >"$backup/production-journal.before.digest"

RELAY_PRODUCTION_ADDITIVE_SOURCE="$backup/staging-source.db" \
 RELAY_PRODUCTION_ADDITIVE_BASE="$backup/production-events.before.db" \
 RELAY_PRODUCTION_ADDITIVE_DESTINATION="$backup/production.candidate.db" \
 RELAY_PRODUCTION_ADDITIVE_MANIFEST="$additions" \
 RELAY_PRODUCTION_ADDITIVE_CONFIRM=production-additive-v1 \
 "$builder" >"$backup/additive-result.json"
python3 - "$backup/additive-result.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: report=json.load(stream)
expected_cities=["91a846c2-b57a-4442-904b-167fb8b98dd6", "a11f3390-9ae9-4e6a-9b54-7464f7f42772"]
expected_kinds={
    "kind::LiveEvent<30311>": 2,
    "kind::TimeCalendarEvent<31923>": 16,
    "kind::unknown<30302>": 2,
    "kind::unknown<30303>": 2,
    "kind::unknown<30304>": 2,
}
if report.get("baseEventCount") != 120 or report.get("addedEventCount") != 24 or report.get("finalEventCount") != 144:
    raise SystemExit("additive promotion did not produce the exact 120+24 event state")
if report.get("cities") != expected_cities or report.get("addedKinds") != expected_kinds:
    raise SystemExit("additive promotion selected an unexpected city or event kind")
if len(set(report.get("addedEventIds", []))) != 24:
    raise SystemExit("additive promotion evidence does not contain 24 unique events")
if report.get("addedEventDigest") != "6a866683bcdff8ef8cf2ef866855d22f33b9c54fdd852b2aa3fed876a3415b92":
    raise SystemExit("additive event set differs from the reviewed Seattle and Hrpelje snapshot")
if report.get("finalEventDigest") != "1e9e4aad53f28fbfa88e6db631fd01c172fef276366a50f1fe94424b7d7c880f":
    raise SystemExit("final production candidate differs from the reviewed 144-event snapshot")
PY

cp -p "$backup/production.candidate.db" "$isolated/events.db"
env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" \
 RELAY_ORGANIZER_MODE=true RELAY_NAME='BitcoinWalk isolated additive production candidate' \
 "$production_binary" >"$backup/isolated.log" 2>&1 &
isolated_pid=$!;wait_health "http://127.0.0.1:$port/healthz"
audit_all "ws://127.0.0.1:$port" "$backup/candidate-audit"
stop_isolated

systemctl stop "$production_service"
activated=true
install -o "$production_uid" -g "$production_gid" -m 0600 "$backup/production.candidate.db" "$production_db"
systemctl reset-failed "$production_service"||true
systemctl start "$production_service";wait_health http://127.0.0.1:3340/healthz
audit_all ws://127.0.0.1:3340 "$backup/production-audit"

systemctl restart "$production_service";wait_health http://127.0.0.1:3340/healthz
audit_all ws://127.0.0.1:3340 "$backup/production-after-restart-audit"
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/nip11.after.json"
grep -q 'bitcoinwalk-production-london-0.8.78' "$backup/nip11.after.json"

"$audit" -source ws://127.0.0.1:3340 -replica "$london_relay" -city "$london" >"$backup/london.after.json"
verify_audit "$backup/london.after.json" "$london"
cp -p "$registry" "$backup/registry.after.json"
cp -p "$ledger" "$backup/entitlements.after.json"
cp -p "$journal" "$backup/production-journal.after.db"
cmp "$backup/registry.before.json" "$backup/registry.after.json"
cmp "$backup/entitlements.before.json" "$backup/entitlements.after.json"
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/production-journal.after.db" "$builder" >"$backup/production-journal.after.digest"
cmp "$backup/production-journal.before.digest" "$backup/production-journal.after.digest"

curl --fail --silent --show-error --max-time 15 https://bitcoinwalk.org/seattle >"$backup/seattle.html"
curl --fail --silent --show-error --max-time 15 https://bitcoinwalk.org/hrpelje >"$backup/hrpelje.html"
grep -q 'BitcoinWalk Seattle' "$backup/seattle.html"
grep -q 'BitcoinWalk Hrpelje' "$backup/hrpelje.html"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3345/api/healthz >"$backup/app-health.after.json"
cmp "$backup/app-health.before.json" "$backup/app-health.after.json"
for unit in "$source_service" "$production_service" bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done

(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
completed=true;trap - EXIT HUP INT TERM
echo "Seattle and Hrpelje additive production promotion accepted on 0.8.87. Backup: $backup"
echo 'Production retains all prior 120 signed events and adds exactly 24 city-scoped events: two sponsor assignments, approvals, revisions, grants and sixteen walks.'
echo 'All fourteen cities remained exact across restart; London replication, the production app and Guide remained healthy and unchanged.'


