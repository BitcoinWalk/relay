#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"

manifest=PRODUCTION-LONDON-REPLICATION-0.8.73-SHA256SUMS
artifact="$root_dir/bitcoinwalk-relay-production-london-0.8.73"
audit="$root_dir/replica-audit-production-london-0.8.73"
promotion_manifest="$root_dir/deploy/production-promotion-0.8.64.json"
dropin_template="$root_dir/deploy/40-production-london-replication-0.8.73.conf"
service=bitcoinwalk-relay-production.service
binary=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay-production/events.db
journal=/var/lib/bitcoinwalk-relay-production/replication-journal.db
config_dir=/etc/bitcoinwalk-replication-production
registry="$config_dir/registry.json"
ledger="$config_dir/entitlements.json"
dropin=/etc/systemd/system/bitcoinwalk-relay-production.service.d/40-london-replication.conf
delivery_key=/etc/bitcoinwalk-replication/replica-delivery-key
staging_registry=/etc/bitcoinwalk-replication/registry.json
staging_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
payments=/var/lib/bitcoinwalk-app-staging/payments.sqlite
city=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
destination=wss://london.bitcoinwalk.org/
expected_evidence=4b54a14253ffcca90eabc01a940781b4539cbdeb45a0147a2e244331e7489672
port=3360

wait_health(){
 local url=$1 ready=false
 for attempt in $(seq 1 40);do
  if curl --fail --silent --max-time 3 "$url" >/dev/null;then ready=true;break;fi
  sleep 1
 done
 "$ready"
}
verify_audit(){
 python3 - "$1" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if not report.get("readOnly") or not report.get("exactEventIds"):
    raise SystemExit("London audit is not exact and read-only")
for side in ("source", "replica"):
    state = report[side]
    if len(state["occurrenceIds"]) != 8 or not state["allSignaturesValid"] or state["privateWrapperCount"] != 0:
        raise SystemExit("London audit is not exact 8/8 public state")
PY
}
audit_public(){
 "$audit" -source wss://relay.bitcoinwalk.org/ -replica "$destination" -city "$city" >"$1"
 verify_audit "$1"
}

sha256sum -c "$manifest"
for command in curl python3 sha256sum mktemp install systemctl journalctl date sed grep stat;do command -v "$command" >/dev/null;done
for file in "$artifact" "$audit" "$promotion_manifest" "$dropin_template" "$binary" "$database" "$delivery_key" "$staging_registry" "$staging_journal" "$payments";do test -f "$file";done
test -x "$artifact";test -x "$audit"
systemctl is-active --quiet "$service"
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-guide.service
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3340/healthz >/dev/null
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3338/api/healthz >/dev/null
test ! -e "$config_dir"
test ! -e "$journal"
test ! -e "$dropin"
! systemctl cat "$service" | grep -q 'RELAY_REPLICA_REGISTRY='

evidence=$(python3 - "$payments" "$city" <<'PY'
import hashlib, sqlite3, sys
path, city = sys.argv[1:]
db = sqlite3.connect("file:" + path + "?mode=ro", uri=True)
db.row_factory = sqlite3.Row
rows = db.execute('SELECT "cityId", "paymentHash", "paidAt" FROM paid_city_entitlement WHERE "cityId" = ?', (city,)).fetchall()
if len(rows) != 1 or not rows[0]["paymentHash"] or int(rows[0]["paidAt"] or 0) <= 0:
    raise SystemExit("London does not have one completed paid-city entitlement")
material = "bitcoinwalk-paid-city-payment-v1:" + city + ":" + rows[0]["paymentHash"]
print(hashlib.sha256(material.encode()).hexdigest())
PY
)
test "$evidence" = "$expected_evidence" || { echo 'London payment evidence commitment changed.' >&2;exit 1; }
unset evidence

backup=$(mktemp -d /var/backups/bitcoinwalk-production-london-replication.XXXXXX);chmod 0700 "$backup"
isolated="$backup/isolated";mkdir "$isolated";chmod 0700 "$isolated"
cp -p "$binary" "$backup/production-binary.before"
cp -p "$database" "$backup/production-events.before.db"
cp -p "$staging_registry" "$backup/staging-registry.before.json"
cp -p "$promotion_manifest" "$backup/production-promotion-manifest.json"
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$staging_journal" "$artifact" >"$backup/staging-journal.before.digest"
systemctl cat "$service" >"$backup/production-service.before.txt"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3338/api/healthz >"$backup/app-health.before.json"
printf '%s\n' "$expected_evidence" >"$backup/payment-evidence.commitment"
chmod 0600 "$backup/payment-evidence.commitment"
echo "Consistent pre-activation production backup created: $backup"

production_stopped=false
isolated_pid=
activated=false
stop_isolated(){
 if test -n "$isolated_pid";then kill "$isolated_pid" >/dev/null 2>&1||true;wait "$isolated_pid" >/dev/null 2>&1||true;isolated_pid=;fi
}
recover(){
 code=$?;trap - EXIT HUP INT TERM;stop_isolated
 if "$activated";then
  systemctl stop "$service" >/dev/null 2>&1||true
  install -o root -g root -m 0755 "$backup/production-binary.before" "$binary"
  rm -f "$dropin" "$journal"
  rm -rf "$config_dir"
  systemctl daemon-reload >/dev/null 2>&1||true
 fi
 systemctl reset-failed "$service" >/dev/null 2>&1||true
 systemctl start "$service" >/dev/null 2>&1||true
 if test "$code" -ne 0;then echo "Production London replication activation did not complete; accepted production service restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

systemctl stop "$service";production_stopped=true
cp -p "$database" "$backup/production-events.consistent.db"
state_uid=$(stat -Lc %u "$database");state_gid=$(stat -Lc %g "$database")

RELAY_PRODUCTION_PROMOTION_SOURCE="$backup/production-events.consistent.db" \
 RELAY_PRODUCTION_PROMOTION_DESTINATION="$backup/production-validation.db" \
 RELAY_PRODUCTION_PROMOTION_MANIFEST="$promotion_manifest" \
 RELAY_PRODUCTION_PROMOTION_CONFIRM=production-promotion-v1 \
 "$artifact" >"$backup/production-validation.json"
python3 - "$backup/production-validation.json" "$city" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: report=json.load(stream)
if report.get("eventCount") != 110 or report.get("paidCities") != [sys.argv[2]] or len(report.get("freeCities", [])) != 11:
    raise SystemExit("production database is not the accepted 12-city, London-only paid state")
PY

RELAY_REPLICA_KEY_INIT="$backup/production-entitlement-authority-key" "$artifact" >"$backup/authority-created.txt"
authority=$(sed -n 's/.*"publicKey": *"\([0-9a-f]*\)".*/\1/p' "$backup/authority-created.txt")
if test "${#authority}" -ne 64;then authority=$(tr -d '\n' <"$backup/authority-created.txt");fi
test "${#authority}" -eq 64
test "$(stat -c %a "$backup/production-entitlement-authority-key")" = 600

RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_KEY="$backup/production-entitlement-authority-key" \
 RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_LEDGER="$backup/entitlements.json" \
 RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_CITY="$city" \
 RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_STATUS=active \
 RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_EVIDENCE="$expected_evidence" \
 RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_CONFIRM=entitlement-production-issue-v1 \
 "$artifact" >"$backup/entitlement-result.json"

RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_REGISTRY="$backup/registry.json" \
 RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_JOURNAL="$backup/replication-journal.empty.db" \
 RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_LEDGER="$backup/entitlements.json" \
 RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_AUTHORITY="$authority" \
 RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_CITY="$city" \
 RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_DESTINATION="$destination" \
 RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_CONFIRM=entitlement-production-bootstrap-v1 \
 "$artifact" >"$backup/bootstrap-result.json"

cp -p "$backup/production-events.consistent.db" "$isolated/events.db"
cp -p "$backup/replication-journal.empty.db" "$isolated/journal.db"
env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" RELAY_ORGANIZER_MODE=true \
 RELAY_REPLICA_JOURNAL="$isolated/journal.db" RELAY_REPLICA_REGISTRY="$backup/registry.json" \
 RELAY_REPLICA_REQUIRE_ENTITLEMENTS=true RELAY_REPLICA_ENTITLEMENT_LEDGER="$backup/entitlements.json" \
 RELAY_REPLICA_ENTITLEMENT_AUTHORITY="$authority" RELAY_NAME='BitcoinWalk isolated London production activation' \
 "$artifact" >"$backup/isolated-start.log" 2>&1 &
isolated_pid=$!
wait_health "http://127.0.0.1:$port/healthz"
stop_isolated
grep -q 'recovered 10 missing record(s)' "$backup/isolated-start.log"

install -d -o root -g root -m 0755 "$config_dir" "$(dirname "$dropin")"
install -o root -g root -m 0644 "$backup/registry.json" "$registry"
install -o root -g root -m 0644 "$backup/entitlements.json" "$ledger"
install -o "$state_uid" -g "$state_gid" -m 0600 "$backup/replication-journal.empty.db" "$journal"
install -o root -g root -m 0755 "$artifact" "$binary"
sed "s/__ENTITLEMENT_AUTHORITY__/$authority/" "$dropin_template" >"$dropin"
chown root:root "$dropin";chmod 0644 "$dropin"
activated=true
systemctl daemon-reload
systemctl reset-failed "$service"||true
activation_since=$(date --iso-8601=seconds)
systemctl start "$service";production_stopped=false
wait_health http://127.0.0.1:3340/healthz
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/nip11.after.json"
grep -q 'bitcoinwalk-production-london-0.8.73' "$backup/nip11.after.json"

delivery_ok=false
for attempt in $(seq 1 30);do
 if audit_public "$backup/london.after.json" 2>/dev/null;then delivery_ok=true;break;fi
 sleep 2
done
"$delivery_ok"
journalctl -u "$service" --since "$activation_since" --no-pager >"$backup/production-activation.log"
grep -q 'Replica acceptance journal enabled for 1 operator-configured paid city relay(s); recovered 10 missing record(s)' "$backup/production-activation.log"
grep -q 'replica delivery acknowledged 8 queued item(s)' "$backup/production-activation.log"
systemctl restart "$service"
wait_health http://127.0.0.1:3340/healthz
audit_public "$backup/london.after-restart.json"
cmp "$backup/london.after.json" "$backup/london.after-restart.json"

sha256sum "$staging_registry" | sed 's#  .*#  staging-registry.json#' >"$backup/staging-registry.after.sha256"
sha256sum "$backup/staging-registry.before.json" | sed 's#  .*#  staging-registry.json#' >"$backup/staging-registry.before.sha256"
cmp "$backup/staging-registry.before.sha256" "$backup/staging-registry.after.sha256"
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$staging_journal" "$artifact" >"$backup/staging-journal.after.digest"
cmp "$backup/staging-journal.before.digest" "$backup/staging-journal.after.digest"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3338/api/healthz >"$backup/app-health.after.json"
cmp "$backup/app-health.before.json" "$backup/app-health.after.json"
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-guide.service

(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
trap - EXIT HUP INT TERM
echo "London production entitlement and registry accepted on 0.8.73. Backup: $backup"
echo 'London is the sole entitled production replica destination and remained exact 8/8 across source restart.'
echo 'The raw payment hash was neither printed nor copied; only its domain-separated SHA-256 commitment was retained.'
echo 'The staging registry, stable staging journal, application, Guide and signed directory chain remained unchanged.'
echo 'The production entitlement authority private key exists only in the root-only backup; preserve an offline recovery copy.'
