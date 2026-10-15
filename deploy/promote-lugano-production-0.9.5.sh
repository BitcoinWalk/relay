#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"

checksums=PRODUCTION-LUGANO-0.9.5-SHA256SUMS
builder="$root_dir/bitcoinwalk-relay-production-additive-0.8.85"
audit="$root_dir/replica-audit-production-additive-0.8.85"
manifest="$root_dir/deploy/production-additive-lugano-0.9.5.json"
source_service=bitcoinwalk-relay.service
production_service=bitcoinwalk-relay-production.service
source_db=/var/lib/bitcoinwalk-relay/events.db
production_db=/var/lib/bitcoinwalk-relay-production/events.db
production_binary=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
registry=/etc/bitcoinwalk-replication-production/registry.json
ledger=/etc/bitcoinwalk-replication-production/entitlements.json
journal=/var/lib/bitcoinwalk-relay-production/replication-journal.db
city=7877ca26-dbe2-4dae-a7f7-483d94ce283a
london=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
hero=e99b21814905774a3a798987f81c5f4f4cc02bf69f8de2b90f5599fbba62119c
port=3364

wait_health(){ local url=$1 ready=false;for attempt in $(seq 1 40);do if curl --fail --silent --max-time 3 "$url" >/dev/null;then ready=true;break;fi;sleep 1;done;"$ready"; }
verify_audit(){ python3 - "$1" "$2" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as stream: report=json.load(stream)
if report.get("cityId")!=sys.argv[2] or not report.get("readOnly") or not report.get("exactEventIds"): raise SystemExit("city audit is not exact and read-only")
if report["source"]["occurrenceIds"]!=report["replica"]["occurrenceIds"]: raise SystemExit("city occurrence IDs differ")
for side in ("source","replica"):
 if not report[side]["allSignaturesValid"] or report[side]["privateWrapperCount"]!=0: raise SystemExit("city audit failed signature or privacy acceptance")
PY
}

sha256sum -c "$checksums"
for command in curl python3 sha256sum mktemp install systemctl stat grep cmp ss;do command -v "$command" >/dev/null;done
for file in "$builder" "$audit" "$manifest" "$source_db" "$production_db" "$production_binary" "$registry" "$ledger" "$journal";do test -f "$file" && test ! -L "$file";done
test -x "$builder";test -x "$audit";test -x "$production_binary"
for unit in "$source_service" "$production_service" bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
curl -fsS --max-time 10 http://127.0.0.1:3334/healthz >/dev/null
curl -fsS --max-time 10 http://127.0.0.1:3340/healthz >/dev/null
curl -fsS --max-time 10 http://127.0.0.1:3345/api/healthz > /tmp/bitcoinwalk-lugano-app-health.$$
grep -q '"status":"ok"' /tmp/bitcoinwalk-lugano-app-health.$$
if ss -ltn | grep -q ":$port ";then echo "Isolated audit port $port is already in use." >&2;exit 1;fi

backup=$(mktemp -d /var/backups/bitcoinwalk-production-lugano.XXXXXX);chmod 0700 "$backup"
mv /tmp/bitcoinwalk-lugano-app-health.$$ "$backup/app-health.before.json"
cp -p "$registry" "$backup/registry.before.json";cp -p "$ledger" "$backup/entitlements.before.json"
curl -fsS --max-time 15 "https://app-staging.bitcoinwalk.org/api/media/files/$hero.webp" >"$backup/lugano-approved-hero.webp"
echo "$hero  $backup/lugano-approved-hero.webp" | sha256sum -c -
isolated="$backup/isolated";mkdir "$isolated";chmod 0700 "$isolated"

isolated_pid=;activated=false;completed=false
stop_isolated(){ if test -n "$isolated_pid";then kill "$isolated_pid" >/dev/null 2>&1||true;wait "$isolated_pid" >/dev/null 2>&1||true;isolated_pid=;fi; }
recover(){ code=$?;trap - EXIT HUP INT TERM;stop_isolated
 systemctl start "$source_service" >/dev/null 2>&1||true
 if "$activated";then systemctl stop "$production_service" >/dev/null 2>&1||true;install -o "$production_uid" -g "$production_gid" -m 0600 "$backup/production-events.before.db" "$production_db";fi
 systemctl start "$production_service" >/dev/null 2>&1||true
 if ! "$completed";then echo "Lugano production promotion failed; prior production database restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

systemctl stop "$source_service";cp -p "$source_db" "$backup/staging-source.db";systemctl start "$source_service";wait_health http://127.0.0.1:3334/healthz
production_uid=$(stat -Lc %u "$production_db");production_gid=$(stat -Lc %g "$production_db")
systemctl stop "$production_service";cp -p "$production_db" "$backup/production-events.before.db";cp -p "$journal" "$backup/production-journal.before.db";systemctl start "$production_service";wait_health http://127.0.0.1:3340/healthz
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/production-journal.before.db" "$builder" >"$backup/production-journal.before.digest"

RELAY_PRODUCTION_ADDITIVE_SOURCE="$backup/staging-source.db" \
 RELAY_PRODUCTION_ADDITIVE_BASE="$backup/production-events.before.db" \
 RELAY_PRODUCTION_ADDITIVE_DESTINATION="$backup/production.candidate.db" \
 RELAY_PRODUCTION_ADDITIVE_MANIFEST="$manifest" RELAY_PRODUCTION_ADDITIVE_CONFIRM=production-additive-v1 \
 "$builder" >"$backup/additive-result.json"
python3 - "$backup/additive-result.json" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as stream: report=json.load(stream)
expected_ids={
 "0010e95112691d97cb62086eae1e4a77d4a3c15dfa99ec30f39b33655e9cf5d8",
 "af9a0f89fac07404ff6b85ae7e7141dee69f7b4620bfbb7f5173561ec24e715d",
 "594acc9a5596665536a1b1dfe4a310238daeadbe5ac45b83403b58f055c01132",
 "072e0143a8acfc6c3ac40f13c67001df84586d98962fd4d965d38dc29fc8affb",
 "45424b887a2e20da23bef6eb72aebaeb89330b1e1ca4518cde9205cfe5b4e767"}
expected_kinds={"kind::LiveEvent<30311>":1,"kind::TimeCalendarEvent<31923>":1,"kind::unknown<30302>":1,"kind::unknown<30303>":1,"kind::unknown<30304>":1}
if report.get("cities")!=["7877ca26-dbe2-4dae-a7f7-483d94ce283a"] or report.get("addedEventCount")!=5 or report.get("addedKinds")!=expected_kinds: raise SystemExit("unexpected Lugano event set")
if set(report.get("addedEventIds",[]))!=expected_ids or report.get("addedEventDigest")!="9d15f16fac77ad2fb77868f41f2cb317f9f4217153e52c847c9de055c1352545": raise SystemExit("Lugano signed IDs differ from review")
if report.get("finalEventCount")!=report.get("baseEventCount",-5)+5: raise SystemExit("production candidate did not preserve the full base")
PY

cp -p "$backup/production.candidate.db" "$isolated/events.db"
env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" RELAY_ORGANIZER_MODE=true RELAY_NAME='BitcoinWalk isolated Lugano candidate' "$production_binary" >"$backup/isolated.log" 2>&1 &
isolated_pid=$!;wait_health "http://127.0.0.1:$port/healthz"
"$audit" -source ws://127.0.0.1:3334 -replica "ws://127.0.0.1:$port" -city "$city" >"$backup/lugano.candidate.json";verify_audit "$backup/lugano.candidate.json" "$city"
"$audit" -source ws://127.0.0.1:3340 -replica "ws://127.0.0.1:$port" -city "$london" >"$backup/london.candidate.json";verify_audit "$backup/london.candidate.json" "$london";stop_isolated

systemctl stop "$production_service";activated=true
install -o "$production_uid" -g "$production_gid" -m 0600 "$backup/production.candidate.db" "$production_db"
systemctl start "$production_service";wait_health http://127.0.0.1:3340/healthz
"$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3340 -city "$city" >"$backup/lugano.production.json";verify_audit "$backup/lugano.production.json" "$city"
systemctl restart "$production_service";wait_health http://127.0.0.1:3340/healthz
"$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3340 -city "$city" >"$backup/lugano.after-restart.json";verify_audit "$backup/lugano.after-restart.json" "$city"

cp -p "$registry" "$backup/registry.after.json";cp -p "$ledger" "$backup/entitlements.after.json";cp -p "$journal" "$backup/production-journal.after.db"
cmp "$backup/registry.before.json" "$backup/registry.after.json";cmp "$backup/entitlements.before.json" "$backup/entitlements.after.json"
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/production-journal.after.db" "$builder" >"$backup/production-journal.after.digest";cmp "$backup/production-journal.before.digest" "$backup/production-journal.after.digest"
curl -fsS --max-time 15 https://bitcoinwalk.org/lugano >"$backup/lugano.html";grep -q 'BitcoinWalk Lugano' "$backup/lugano.html"
curl -fsS --max-time 15 https://bitcoinwalk.org/api/cities >"$backup/cities.json"
python3 - "$backup/cities.json" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as stream: data=json.load(stream)
rows=[row for row in data.get("cities",[]) if row.get("id")=="7877ca26-dbe2-4dae-a7f7-483d94ce283a"]
if len(rows)!=1 or rows[0].get("slug")!="lugano": raise SystemExit("production city directory does not expose Lugano")
city=(rows[0].get("chat") or {}).get("nip29") or {}
if city.get("relay")!="wss://chat.bitcoinwalk.org/" or city.get("groupId")!="bitcoinwalk-lugano": raise SystemExit("Lugano Walkie Talkie routing is incomplete")
PY
curl -fsS --max-time 10 http://127.0.0.1:3345/api/healthz >"$backup/app-health.after.json";cmp "$backup/app-health.before.json" "$backup/app-health.after.json"
for unit in "$source_service" "$production_service" bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
completed=true;trap - EXIT HUP INT TERM
echo "Lugano additive production promotion accepted on 0.9.5. Backup: $backup"
echo "Production retained every prior signed event and added exactly five reviewed Lugano records. Lugano remains Basic; payment and paid-city replication state are unchanged."
echo "The canonical city page, 25 October walk and verified Walkie Talkie routing remained exact across production restart."
