#!/bin/sh
# Prove the signed-entitlement plan/apply/startup boundary in an isolated
# staging city. This creates no payment claim and installs no live registry.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-ENTITLEMENT-REHEARSAL-0.8.31-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-entitlement-0.8.31
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
registry=/etc/bitcoinwalk-replication/registry.json
authority_key=/etc/bitcoinwalk-replication/staging-entitlement-authority-key
base_city=73d20526-6bbf-4ed1-a98e-e7fd77de8179
base_destination=wss://synthetic-baseline.invalid/
city=8b6f3742-64b8-4d77-915a-a76b375fa06a
destination=wss://synthetic-entitlement.invalid/
port=3357
accepted_source=b1b789170d02969ec72aac6e90a792219aa20b714e86dc93ce31187c5b5ee8ac

digest(){ sha256sum "$1" | cut -d ' ' -f 1; }
bolt_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }
wait_health(){
 attempt=0
 until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null;do
  attempt=$((attempt+1));test "$attempt" -lt 30||return 1;sleep 1
 done
}
wait_source_health(){
 attempt=0
 until curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz >/dev/null;do
  attempt=$((attempt+1));test "$attempt" -lt 30||return 1;sleep 1
 done
}

sha256sum -c "$manifest"
test "$(digest "$source_target")" = "$accepted_source"
for file in "$source_db" "$source_journal" "$registry";do test -f "$file";done
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done
! ss -ltn "sport = :$port" | grep -q LISTEN

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-entitlement-rehearsal.XXXXXX)
isolated=$backup/isolated
mkdir "$isolated"
chmod 0700 "$backup" "$isolated"
completed=0
isolated_pid=
stop_isolated(){
 if [ -n "$isolated_pid" ];then
  kill "$isolated_pid" >/dev/null 2>&1||true
  wait "$isolated_pid" >/dev/null 2>&1||true
  isolated_pid=
 fi
}
recover(){
 code=$?;trap - EXIT HUP INT TERM
 stop_isolated
 systemctl reset-failed bitcoinwalk-relay.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-relay.service >/dev/null 2>&1||true
 if [ "$completed" -ne 1 ];then
  echo "Entitlement rehearsal did not complete; live registry and journal were not changed. Backup: $backup" >&2
 fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

systemctl stop bitcoinwalk-relay.service
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$registry" "$backup/live-registry.json"
cp -p "$source_target" "$backup/live-source-binary"
if [ -e "$authority_key" ];then cp -p "$authority_key" "$backup/preexisting-staging-authority-key";fi
(
 cd "$backup"
 sha256sum source-events.db source-replication-journal.db live-registry.json live-source-binary >SHA256SUMS
 if [ -e preexisting-staging-authority-key ];then sha256sum preexisting-staging-authority-key >>SHA256SUMS;fi
)
journal_digest_before=$(bolt_digest "$backup/source-replication-journal.db")
echo "Consistent pre-rehearsal backup created: $backup"
systemctl start bitcoinwalk-relay.service
wait_source_health

if [ ! -e "$authority_key" ];then
 umask 077
 RELAY_REPLICA_KEY_INIT="$authority_key" "$artifact" >"$backup/authority-created.txt"
fi
test -f "$authority_key"
test "$(stat -c %a "$authority_key")" = 600

RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_KEY="$authority_key" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_LEDGER="$isolated/entitlements.base.json" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CITY="$base_city" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_STATUS=active RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CONFIRM=entitlement-staging-issue-v1 "$artifact" >"$backup/base-entitlement-result.json"
authority=$(sed -n 's/.*"authority": "\([0-9a-f]*\)".*/\1/p' "$backup/base-entitlement-result.json")
base_event=$(sed -n 's/.*"entitlementEventId": "\([0-9a-f]*\)".*/\1/p' "$backup/base-entitlement-result.json")
test "${#authority}" -eq 64
test "${#base_event}" -eq 64
printf '{\n  "version": 1,\n  "cities": [\n    {\n      "cityId": "%s",\n      "destination": "%s",\n      "entitlementEventId": "%s"\n    }\n  ]\n}\n' "$base_city" "$base_destination" "$base_event" >"$isolated/registry.base.json"
chmod 0600 "$isolated/registry.base.json"

env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" RELAY_ORGANIZER_MODE=true RELAY_REPLICA_JOURNAL="$isolated/journal.db" RELAY_REPLICA_REGISTRY="$isolated/registry.base.json" RELAY_REPLICA_REQUIRE_ENTITLEMENTS=true RELAY_REPLICA_ENTITLEMENT_LEDGER="$isolated/entitlements.base.json" RELAY_REPLICA_ENTITLEMENT_AUTHORITY="$authority" RELAY_NAME='BitcoinWalk isolated entitlement rehearsal' "$artifact" >"$backup/initialize.log" 2>&1 &
isolated_pid=$!
wait_health
stop_isolated

RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_KEY="$authority_key" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_SOURCE="$isolated/entitlements.base.json" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_LEDGER="$isolated/entitlements.active.json" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CITY="$city" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_STATUS=active RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CONFIRM=entitlement-staging-issue-v1 "$artifact" >"$backup/active-entitlement-result.json"

RELAY_REPLICA_ENTITLEMENT_PLAN_CURRENT="$isolated/registry.base.json" RELAY_REPLICA_ENTITLEMENT_PLAN_LEDGER="$isolated/entitlements.active.json" RELAY_REPLICA_ENTITLEMENT_PLAN_CANDIDATE="$isolated/registry.candidate.json" RELAY_REPLICA_ENTITLEMENT_PLAN_AUTHORITY="$authority" RELAY_REPLICA_ENTITLEMENT_PLAN_CITY="$city" RELAY_REPLICA_ENTITLEMENT_PLAN_DESTINATION="$destination" RELAY_REPLICA_ENTITLEMENT_PLAN_CONFIRM=entitlement-provision-v1 "$artifact" >"$backup/plan-result.json"

RELAY_REPLICA_ENTITLEMENT_APPLY_JOURNAL="$isolated/journal.db" RELAY_REPLICA_ENTITLEMENT_APPLY_CURRENT="$isolated/registry.base.json" RELAY_REPLICA_ENTITLEMENT_APPLY_LEDGER="$isolated/entitlements.active.json" RELAY_REPLICA_ENTITLEMENT_APPLY_CANDIDATE="$isolated/registry.candidate.json" RELAY_REPLICA_ENTITLEMENT_APPLY_AUTHORITY="$authority" RELAY_REPLICA_ENTITLEMENT_APPLY_CITY="$city" RELAY_REPLICA_ENTITLEMENT_APPLY_CONFIRM=entitlement-apply-v1 "$artifact" >"$backup/apply-result.json"
cp -p "$isolated/registry.candidate.json" "$isolated/registry.active.json"

start_entitled(){
 ledger=$1;log=$2
 env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" RELAY_ORGANIZER_MODE=true RELAY_REPLICA_JOURNAL="$isolated/journal.db" RELAY_REPLICA_REGISTRY="$isolated/registry.active.json" RELAY_REPLICA_REQUIRE_ENTITLEMENTS=true RELAY_REPLICA_ENTITLEMENT_LEDGER="$ledger" RELAY_REPLICA_ENTITLEMENT_AUTHORITY="$authority" RELAY_NAME='BitcoinWalk isolated entitled source' "$artifact" >"$log" 2>&1 &
 isolated_pid=$!
}

start_entitled "$isolated/entitlements.active.json" "$backup/active-start-1.log"
wait_health
stop_isolated
start_entitled "$isolated/entitlements.active.json" "$backup/active-start-2.log"
wait_health
stop_isolated

sleep 1
RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_KEY="$authority_key" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_SOURCE="$isolated/entitlements.active.json" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_LEDGER="$isolated/entitlements.revoked.json" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CITY="$city" RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_STATUS=revoked RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CONFIRM=entitlement-staging-issue-v1 "$artifact" >"$backup/revoked-entitlement-result.json"

if env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" RELAY_ORGANIZER_MODE=true RELAY_REPLICA_JOURNAL="$isolated/journal.db" RELAY_REPLICA_REGISTRY="$isolated/registry.active.json" RELAY_REPLICA_REQUIRE_ENTITLEMENTS=true RELAY_REPLICA_ENTITLEMENT_LEDGER="$isolated/entitlements.revoked.json" RELAY_REPLICA_ENTITLEMENT_AUTHORITY="$authority" "$artifact" >"$backup/revoked-start.log" 2>&1;then
 echo 'Revoked synthetic entitlement unexpectedly started.' >&2;exit 1
fi
grep -q 'absent, revoked or superseded' "$backup/revoked-start.log"

start_entitled "$isolated/entitlements.active.json" "$backup/active-start-after-rejection.log"
wait_health
stop_isolated

test "$(digest "$registry")" = "$(digest "$backup/live-registry.json")"
systemctl stop bitcoinwalk-relay.service
journal_digest_after=$(bolt_digest "$source_journal")
systemctl start bitcoinwalk-relay.service
wait_source_health
test "$journal_digest_after" = "$journal_digest_before"
systemctl is-active --quiet bitcoinwalk-relay.service
(
 cd "$backup"
 sha256sum base-entitlement-result.json active-entitlement-result.json plan-result.json apply-result.json revoked-entitlement-result.json active-start-1.log active-start-2.log revoked-start.log active-start-after-rejection.log isolated/registry.base.json isolated/registry.candidate.json isolated/registry.active.json isolated/entitlements.base.json isolated/entitlements.active.json isolated/entitlements.revoked.json isolated/events.db isolated/journal.db >>SHA256SUMS
)
completed=1
trap - EXIT HUP INT TERM
echo "Isolated synthetic entitlement provisioning accepted. Backup: $backup"
echo "Staging entitlement authority public key: $authority"
echo "Synthetic city: $city"
echo 'Plan, apply, two accepted starts and one revoked fail-closed start passed; no live registry, journal, relay database, DNS or payment state changed.'
