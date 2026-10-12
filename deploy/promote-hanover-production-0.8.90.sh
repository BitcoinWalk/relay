#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"

checksums=PRODUCTION-HANOVER-0.8.90-SHA256SUMS
builder="$root_dir/bitcoinwalk-relay-production-additive-0.8.85"
audit="$root_dir/replica-audit-production-additive-0.8.85"
manifest="$root_dir/deploy/production-additive-hanover-0.8.90.json"
source_service=bitcoinwalk-relay.service
production_service=bitcoinwalk-relay-production.service
source_db=/var/lib/bitcoinwalk-relay/events.db
production_db=/var/lib/bitcoinwalk-relay-production/events.db
production_binary=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
registry=/etc/bitcoinwalk-replication-production/registry.json
ledger=/etc/bitcoinwalk-replication-production/entitlements.json
journal=/var/lib/bitcoinwalk-relay-production/replication-journal.db
city=d42123e1-1d01-473b-98e7-846342150eca
london=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
london_relay=wss://london.bitcoinwalk.org/
port=3363

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
for command in curl python3 sha256sum mktemp install systemctl stat grep cmp;do command -v "$command" >/dev/null;done
for file in "$builder" "$audit" "$manifest" "$source_db" "$production_db" "$production_binary" "$registry" "$ledger" "$journal";do test -f "$file" && test ! -L "$file";done
for unit in "$source_service" "$production_service" bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
curl -fsS --max-time 10 http://127.0.0.1:3334/healthz >/dev/null
curl -fsS --max-time 10 http://127.0.0.1:3340/healthz >/dev/null
curl -fsS --max-time 10 http://127.0.0.1:3345/api/healthz > /tmp/bitcoinwalk-hanover-app-health.$$
grep -q '"status":"ok"' /tmp/bitcoinwalk-hanover-app-health.$$

backup=$(mktemp -d /var/backups/bitcoinwalk-production-hanover.XXXXXX);chmod 0700 "$backup"
mv /tmp/bitcoinwalk-hanover-app-health.$$ "$backup/app-health.before.json"
cp -p "$registry" "$backup/registry.before.json";cp -p "$ledger" "$backup/entitlements.before.json"
isolated="$backup/isolated";mkdir "$isolated";chmod 0700 "$isolated"

isolated_pid=;activated=false;completed=false
stop_isolated(){ if test -n "$isolated_pid";then kill "$isolated_pid" >/dev/null 2>&1||true;wait "$isolated_pid" >/dev/null 2>&1||true;isolated_pid=;fi; }
recover(){ code=$?;trap - EXIT HUP INT TERM;stop_isolated
 systemctl start "$source_service" >/dev/null 2>&1||true
 if "$activated";then systemctl stop "$production_service" >/dev/null 2>&1||true;install -o "$production_uid" -g "$production_gid" -m 0600 "$backup/production-events.before.db" "$production_db";fi
 systemctl start "$production_service" >/dev/null 2>&1||true
 if ! "$completed";then echo "Hanover production promotion failed; prior production database restored. Backup: $backup" >&2;fi
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
 "3f928ef1b4f161f70117c08f9dce8e94a2c385c5d705f0f2c4635195606daad0",
 "7629e2c8a1d3eac08a591d0e659b552baa987d572d3c6e93308202ca4b7249ac",
 "872cef7e804c25df22d449536d2d8426456b9b705000f27c10379b2bc428e86c",
 "d1c88af151144ccd5577cefd1a67b8bab32a0a316cf698da98e054b5c437e791",
 "e1ebfc085f3bcad06bceab301759585ec62268dba6c06faf62a6a2e22723900b",
 "a3125272ed89f5c6c04416d26b790f64a32f47d2bbb39e2bbe14d8c7c22ca79c",
 "a2187a3a48f4cb7451ea0f27c6d0af92d608608ba9730652bd4c02b77624589d",
 "eca18f04548279a7985d4e654792a286390d2a82feb5c86e79c9ac090524cd30",
 "750f9dbf4e14cad5ac683772e318a46efe18e3a0fc5831d7eb021c21f92d69a3",
 "10b6df89108fd083a53b59f5d4004456bb36d15c44e4307d02469bb3b8568348",
 "7d53ad2bd7d50c390f2531bab25ffc1e07729e69272feb7d9709067f82bfb2b3",
 "a48dc41b1c3db89cc7128bb78263f92402dc3701da921fa6e7179ac37a9e3507"}
expected_kinds={"kind::LiveEvent<30311>":1,"kind::TimeCalendarEvent<31923>":8,"kind::unknown<30302>":1,"kind::unknown<30303>":1,"kind::unknown<30304>":1}
if report.get("cities")!=["d42123e1-1d01-473b-98e7-846342150eca"] or report.get("addedEventCount")!=12 or report.get("addedKinds")!=expected_kinds: raise SystemExit("unexpected Hanover event set")
if set(report.get("addedEventIds",[]))!=expected_ids or report.get("addedEventDigest")!="f5c4d5007fd850a9f96de7ffdc49b950a15f023ab7c0eb8306c0aaf9367f2e2d": raise SystemExit("Hanover signed IDs differ from review")
if report.get("finalEventCount")!=report.get("baseEventCount",-12)+12: raise SystemExit("production candidate did not preserve the full base")
PY

cp -p "$backup/production.candidate.db" "$isolated/events.db"
env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" RELAY_ORGANIZER_MODE=true RELAY_NAME='BitcoinWalk isolated Hanover candidate' "$production_binary" >"$backup/isolated.log" 2>&1 &
isolated_pid=$!;wait_health "http://127.0.0.1:$port/healthz"
"$audit" -source ws://127.0.0.1:3334 -replica "ws://127.0.0.1:$port" -city "$city" >"$backup/hanover.candidate.json";verify_audit "$backup/hanover.candidate.json" "$city";stop_isolated

systemctl stop "$production_service";activated=true
install -o "$production_uid" -g "$production_gid" -m 0600 "$backup/production.candidate.db" "$production_db"
systemctl start "$production_service";wait_health http://127.0.0.1:3340/healthz
"$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3340 -city "$city" >"$backup/hanover.production.json";verify_audit "$backup/hanover.production.json" "$city"
systemctl restart "$production_service";wait_health http://127.0.0.1:3340/healthz
"$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3340 -city "$city" >"$backup/hanover.after-restart.json";verify_audit "$backup/hanover.after-restart.json" "$city"

"$audit" -source ws://127.0.0.1:3340 -replica "$london_relay" -city "$london" >"$backup/london.after.json";verify_audit "$backup/london.after.json" "$london"
cp -p "$registry" "$backup/registry.after.json";cp -p "$ledger" "$backup/entitlements.after.json";cp -p "$journal" "$backup/production-journal.after.db"
cmp "$backup/registry.before.json" "$backup/registry.after.json";cmp "$backup/entitlements.before.json" "$backup/entitlements.after.json"
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/production-journal.after.db" "$builder" >"$backup/production-journal.after.digest";cmp "$backup/production-journal.before.digest" "$backup/production-journal.after.digest"
curl -fsS --max-time 15 https://bitcoinwalk.org/hanover >"$backup/hanover.html";grep -q 'BitcoinWalk Hanover' "$backup/hanover.html"
curl -fsS --max-time 10 http://127.0.0.1:3345/api/healthz >"$backup/app-health.after.json";cmp "$backup/app-health.before.json" "$backup/app-health.after.json"
for unit in "$source_service" "$production_service" bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
completed=true;trap - EXIT HUP INT TERM
echo "Hanover additive production promotion accepted. Backup: $backup"
echo "Production retained every prior signed event and added exactly twelve reviewed Hanover events. Payment state remains unchanged by this relay operation."
