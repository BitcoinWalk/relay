#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"

manifest=PRODUCTION-PROMOTION-ACTIVATION-0.8.76-SHA256SUMS
artifact="$root_dir/bitcoinwalk-relay-production-promotion-0.8.76"
audit="$root_dir/replica-audit-production-promotion-0.8.76"
promotion_manifest="$root_dir/deploy/production-promotion-0.8.64.json"
source_service=bitcoinwalk-relay.service
production_service=bitcoinwalk-relay-production.service
source_db=/var/lib/bitcoinwalk-relay/events.db
production_db=/var/lib/bitcoinwalk-relay-production/events.db
production_binary=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
version_dropin=/etc/systemd/system/bitcoinwalk-relay-production.service.d/90-production-version.conf
expected_event_digest=7000b1ac878981235fb75360f3aa9f0c0215e1bfb73e85b2196066853ce8fd4e
expected_london=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
port=3361

wait_health(){
 local url=$1 ready=false
 for attempt in $(seq 1 40);do
  if curl --fail --silent --max-time 3 "$url" >/dev/null;then ready=true;break;fi
  sleep 1
 done
 "$ready"
}
verify_report(){
 python3 - "$1" "$2" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: report=json.load(stream)
if report.get("cityId") != sys.argv[2] or not report.get("readOnly") or not report.get("exactEventIds"):
    raise SystemExit("city promotion audit is not exact and read-only")
if report["source"]["occurrenceIds"] != report["replica"]["occurrenceIds"]:
    raise SystemExit("city occurrence IDs differ after promotion")
for side in ("source", "replica"):
    if not report[side]["allSignaturesValid"] or report[side]["privateWrapperCount"] != 0:
        raise SystemExit("city promotion failed signature or privacy acceptance")
PY
}
audit_all(){
 local replica=$1 prefix=$2
 python3 - "$promotion_manifest" <<'PY' | while read -r city;do
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: manifest=json.load(stream)
for city in manifest["cities"]: print(city["cityId"])
PY
   report="$prefix.$city.json"
   "$audit" -source ws://127.0.0.1:3334 -replica "$replica" -city "$city" -allow-empty >"$report"
   verify_report "$report" "$city"
 done
}

sha256sum -c "$manifest"
for command in curl python3 sha256sum mktemp install systemctl stat;do command -v "$command" >/dev/null;done
for file in "$artifact" "$audit" "$promotion_manifest" "$source_db" "$production_db" "$production_binary";do test -f "$file";done
test -x "$artifact";test -x "$audit"
for unit in "$source_service" "$production_service" bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3338/api/healthz >/dev/null
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3334/healthz >/dev/null
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3340/healthz >/dev/null
test ! -e /etc/bitcoinwalk-replication-production
test ! -e /var/lib/bitcoinwalk-relay-production/replication-journal.db
! systemctl cat "$production_service" | grep -q 'RELAY_REPLICA_REGISTRY='

backup=$(mktemp -d /var/backups/bitcoinwalk-production-promotion-activation.XXXXXX);chmod 0700 "$backup"
isolated="$backup/isolated";mkdir "$isolated";chmod 0700 "$isolated"
completed=false;activated=false;isolated_pid=
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
  install -o root -g root -m 0755 "$backup/production-binary.before" "$production_binary"
  if test -f "$backup/production-version.before";then
   install -d -o root -g root -m 0755 "$(dirname "$version_dropin")"
   install -o root -g root -m 0644 "$backup/production-version.before" "$version_dropin"
  else
   rm -f "$version_dropin"
  fi
  systemctl daemon-reload >/dev/null 2>&1||true
 fi
 systemctl reset-failed "$production_service" >/dev/null 2>&1||true
 systemctl start "$production_service" >/dev/null 2>&1||true
 if ! "$completed";then echo "Production promotion activation did not complete; prior production database restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

systemctl stop "$source_service"
cp -p "$source_db" "$backup/staging-source.db"
systemctl start "$source_service"
wait_health http://127.0.0.1:3334/healthz

RELAY_PRODUCTION_PROMOTION_SOURCE="$backup/staging-source.db" \
 RELAY_PRODUCTION_PROMOTION_DESTINATION="$backup/production.candidate.db" \
 RELAY_PRODUCTION_PROMOTION_MANIFEST="$promotion_manifest" \
 RELAY_PRODUCTION_PROMOTION_CONFIRM=production-promotion-v1 \
 "$artifact" >"$backup/promotion-result.json"
python3 - "$backup/promotion-result.json" "$expected_event_digest" "$expected_london" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: report=json.load(stream)
if report.get("eventDigest") != sys.argv[2] or report.get("eventCount") != 110:
    raise SystemExit("current staging state differs from the accepted promotion rehearsal")
if report.get("paidCities") != [sys.argv[3]] or len(report.get("freeCities", [])) != 11:
    raise SystemExit("London is not the sole paid city in the promotion candidate")
PY

cp -p "$backup/production.candidate.db" "$isolated/events.db"
env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" \
 RELAY_ORGANIZER_MODE=true RELAY_NAME='BitcoinWalk isolated production promotion candidate' \
 "$artifact" >"$backup/isolated.log" 2>&1 &
isolated_pid=$!
wait_health "http://127.0.0.1:$port/healthz"
audit_all "ws://127.0.0.1:$port" "$backup/candidate-audit"
stop_isolated

production_uid=$(stat -Lc %u "$production_db");production_gid=$(stat -Lc %g "$production_db")
systemctl stop "$production_service"
cp -p "$production_db" "$backup/production-events.before.db"
cp -p "$production_binary" "$backup/production-binary.before"
test ! -f "$version_dropin"||cp -p "$version_dropin" "$backup/production-version.before"
activated=true
install -o "$production_uid" -g "$production_gid" -m 0600 "$backup/production.candidate.db" "$production_db"
install -o root -g root -m 0755 "$artifact" "$production_binary"
install -d -o root -g root -m 0755 "$(dirname "$version_dropin")"
printf '[Service]\nEnvironment=RELAY_VERSION=bitcoinwalk-production-0.8.76\n' >"$version_dropin"
chown root:root "$version_dropin";chmod 0644 "$version_dropin"
systemctl daemon-reload
systemctl reset-failed "$production_service"||true
systemctl start "$production_service"
wait_health http://127.0.0.1:3340/healthz
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/nip11.after.json"
grep -q 'bitcoinwalk-production-0.8.76' "$backup/nip11.after.json"
audit_all ws://127.0.0.1:3340 "$backup/production-audit"
curl --fail --silent --show-error --max-time 15 -H 'Accept: application/nostr+json' https://relay.bitcoinwalk.org/ >"$backup/public-nip11.json"

systemctl restart "$production_service"
wait_health http://127.0.0.1:3340/healthz
audit_all ws://127.0.0.1:3340 "$backup/production-after-restart-audit"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3338/api/healthz >"$backup/app-health.after.json"
for unit in "$source_service" "$production_service" bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done

(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
completed=true
trap - EXIT HUP INT TERM
echo "Atomic 12-city production promotion accepted on 0.8.76. Backup: $backup"
echo 'The current staging state matched the rehearsed 110-event digest; London is the sole paid city and eleven cities remain free.'
echo 'All city occurrence sets remained exact across production restart; the application, Guide, DNS, Caddy and signed directory were unchanged.'
echo 'Production replication is still disabled; run the separate London entitlement activation next.'
