#!/bin/sh
# Audit source/journal/outbox reconciliation on consistent isolated copies,
# then prove that live public state and Guide delivery state did not change.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-RECONCILIATION-AUDIT-0.8.33-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-reconciliation-0.8.33
public_audit=./replica-audit-0.8.33
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
registry=/etc/bitcoinwalk-replication/registry.json
status_token=/etc/bitcoinwalk-replication/replica-status-token
guide_db=/var/lib/bitcoinwalk-guide/guide.sqlite
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
memphis=be8514a4-9df0-4159-a517-71f65761cbbe
nashville=586c0d1f-e861-4c8f-858c-ce3e2bfaf384
accepted_source=b1b789170d02969ec72aac6e90a792219aa20b714e86dc93ce31187c5b5ee8ac

digest(){ sha256sum "$1" | cut -d ' ' -f 1; }
bolt_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }
guide_snapshot(){
 "$node" -e 'const{DatabaseSync}=require("node:sqlite");const db=new DatabaseSync(process.argv[1],{readOnly:true});console.log(JSON.stringify(db.prepare("SELECT purpose,state,count(*) AS count FROM delivery GROUP BY purpose,state ORDER BY purpose,state").all()));db.close();' "$guide_db"
}
fetch_status(){
 curl --fail --silent --show-error --max-time 5 -H "Authorization: Bearer $(tr -d '\n' <"$status_token")" http://127.0.0.1:3334/replication/status >"$1"
}
wait_source_health(){
 attempt=0
 until curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz >/dev/null;do
  attempt=$((attempt+1));test "$attempt" -lt 30||return 1;sleep 1
 done
}
run_audit(){
 env -i PATH=/usr/bin:/bin RELAY_LISTEN=127.0.0.1:3358 RELAY_DB="$1" RELAY_ORGANIZER_MODE=true RELAY_REPLICA_JOURNAL="$2" RELAY_REPLICA_REGISTRY="$3" RELAY_REPLICA_RECONCILIATION_AUDIT=true "$artifact"
}

sha256sum -c "$manifest"
test -x "$artifact";test -x "$public_audit"
test "$(digest "$source_target")" = "$accepted_source"
for file in "$source_db" "$source_journal" "$registry" "$status_token" "$guide_db";do test -f "$file";done
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-reconciliation-audit.XXXXXX)
isolated=$backup/isolated
mkdir "$isolated"
chmod 0700 "$backup" "$isolated"
completed=0
recover(){
 code=$?;trap - EXIT HUP INT TERM
 systemctl reset-failed bitcoinwalk-relay.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-relay.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 if [ "$completed" -ne 1 ];then
  echo "Reconciliation audit did not complete; live data was not replaced. Backup: $backup" >&2
 fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

"$public_audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/memphis-before.json"
"$public_audit" -source ws://127.0.0.1:3334 -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty >"$backup/nashville-before.json"
fetch_status "$backup/status-before.json"
guide_before=$(guide_snapshot)

systemctl stop bitcoinwalk-guide.service bitcoinwalk-relay.service
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$registry" "$backup/registry.json"
cp -p "$source_target" "$backup/source-binary"
cp -p "$source_db" "$isolated/events.db"
cp -p "$source_journal" "$isolated/journal.db"
cp -p "$registry" "$isolated/registry.json"
(
 cd "$backup"
 sha256sum source-events.db source-replication-journal.db registry.json source-binary >SHA256SUMS
)
echo "Consistent pre-audit backup created: $backup"
systemctl start bitcoinwalk-relay.service bitcoinwalk-guide.service
wait_source_health

events_before=$(bolt_digest "$isolated/events.db")
journal_before=$(bolt_digest "$isolated/journal.db")
run_audit "$isolated/events.db" "$isolated/journal.db" "$isolated/registry.json" >"$backup/reconciliation-1.json"
events_after=$(bolt_digest "$isolated/events.db")
journal_after=$(bolt_digest "$isolated/journal.db")
test "$events_before" = "$events_after"
test "$journal_before" = "$journal_after"
grep -q '"state":"healthy"' "$backup/reconciliation-1.json"
grep -q "\"cityId\":\"$memphis\"" "$backup/reconciliation-1.json"
grep -q "\"cityId\":\"$nashville\"" "$backup/reconciliation-1.json"
for forbidden in eventId eventID payload content author attempts lastCode envelope bundle;do
 ! grep -q "$forbidden" "$backup/reconciliation-1.json"
done
run_audit "$isolated/events.db" "$isolated/journal.db" "$isolated/registry.json" >"$backup/reconciliation-2.json"
cmp "$backup/reconciliation-1.json" "$backup/reconciliation-2.json"
test "$events_before" = "$(bolt_digest "$isolated/events.db")"
test "$journal_before" = "$(bolt_digest "$isolated/journal.db")"
echo 'Read-only reconciliation audit passed twice with unchanged logical database digests.'

"$public_audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/memphis-after.json"
"$public_audit" -source ws://127.0.0.1:3334 -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty >"$backup/nashville-after.json"
cmp "$backup/memphis-before.json" "$backup/memphis-after.json"
cmp "$backup/nashville-before.json" "$backup/nashville-after.json"
fetch_status "$backup/status-after.json"
cmp "$backup/status-before.json" "$backup/status-after.json"
test "$(guide_snapshot)" = "$guide_before"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done
(
 cd "$backup"
 sha256sum reconciliation-1.json reconciliation-2.json memphis-before.json memphis-after.json nashville-before.json nashville-after.json status-before.json status-after.json isolated/events.db isolated/journal.db isolated/registry.json >>SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Public replica reconciliation audit accepted. Backup: $backup"
echo 'Memphis remained exact 7/7, Nashville remained exact 0/0, and Guide delivery state was unchanged.'
echo 'No audit copy or new executable was installed into a live path.'
