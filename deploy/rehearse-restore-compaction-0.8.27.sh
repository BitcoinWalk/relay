#!/bin/sh
# Exercise recovery from consistent source and receiver snapshots, compact only
# isolated copies, and prove that cancellation/revocation filtering survives.
# This script never installs a compacted database into a live path.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-RESTORE-COMPACTION-0.8.27-SHA256SUMS
maintenance=./bitcoinwalk-relay-replica-maintenance-0.8.27
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
memphis_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
nashville_target=/opt/bitcoinwalk-replica-firstwalk/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
memphis_db=/var/lib/bitcoinwalk-replica-rehearsal/events.db
nashville_db=/var/lib/bitcoinwalk-replica-firstwalk/events.db
registry=/etc/bitcoinwalk-replication/registry.json
memphis_config=/etc/bitcoinwalk-replication/receiver.conf
nashville_config=/etc/bitcoinwalk-replication/receiver-firstwalk.conf
status_token=/etc/bitcoinwalk-replication/replica-status-token
guide_db=/var/lib/bitcoinwalk-guide/guide.sqlite
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
memphis=be8514a4-9df0-4159-a517-71f65761cbbe
nashville=586c0d1f-e861-4c8f-858c-ce3e2bfaf384
accepted_source=b1b789170d02969ec72aac6e90a792219aa20b714e86dc93ce31187c5b5ee8ac
accepted_receiver=0719f7e05f1127cd33509a6b196b81c2b2a235d759956673d931757ef09af7a4

digest(){ sha256sum "$1" | cut -d ' ' -f 1; }
guide_snapshot(){
 "$node" -e 'const{DatabaseSync}=require("node:sqlite");const db=new DatabaseSync(process.argv[1],{readOnly:true});console.log(JSON.stringify(db.prepare("SELECT purpose,state,count(*) AS count FROM delivery GROUP BY purpose,state ORDER BY purpose,state").all()));db.close();' "$guide_db"
}
fetch_status(){
 curl --fail --silent --show-error --max-time 5 -H "Authorization: Bearer $(tr -d '\n' <"$status_token")" http://127.0.0.1:3334/replication/status >"$1"
}
wait_health(){
 port=$1;attempt=0
 until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null;do
  attempt=$((attempt+1));test "$attempt" -lt 30||return 1;sleep 1
 done
}
compact(){
 RELAY_REPLICA_DB_COMPACT_SOURCE="$1" RELAY_REPLICA_DB_COMPACT_DESTINATION="$2" "$maintenance"
}
bolt_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$maintenance"; }
digest_equal(){ test "$(bolt_digest "$1")" = "$(bolt_digest "$2")"; }
audit_equal(){
 "$node" -e 'const fs=require("node:fs");const a=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));const b=JSON.parse(fs.readFileSync(process.argv[2],"utf8"));const view=x=>({cityId:x.cityId,readOnly:x.readOnly,exactEventIds:x.exactEventIds,allowEmpty:x.allowEmpty,sourceIds:x.source.occurrenceIds,sourceSignatures:x.source.allSignaturesValid,sourceWrappers:x.source.privateWrapperCount,replicaIds:x.replica.occurrenceIds,replicaSignatures:x.replica.allSignaturesValid,replicaWrappers:x.replica.privateWrapperCount});if(JSON.stringify(view(a))!==JSON.stringify(view(b)))process.exit(1);' "$1" "$2"
}

sha256sum -c "$manifest"
test "$(digest "$source_target")" = "$accepted_source"
test "$(digest "$memphis_target")" = "$accepted_receiver"
test "$(digest "$nashville_target")" = "$accepted_receiver"
for file in "$source_db" "$source_journal" "$memphis_db" "$nashville_db" "$registry" "$memphis_config" "$nashville_config" "$status_token" "$guide_db";do test -f "$file";done
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done
for port in 3354 3355 3356;do ! ss -ltn "sport = :$port" | grep -q LISTEN;done

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-restore-compaction.XXXXXX)
restore=$backup/restore
mkdir "$restore"
chmod 0700 "$backup" "$restore"
completed=0
isolated_pids=
kill_isolated(){
 if [ -n "$isolated_pids" ];then
  kill $isolated_pids >/dev/null 2>&1||true
  wait $isolated_pids >/dev/null 2>&1||true
  isolated_pids=
 fi
}
recover(){
 code=$?;trap - EXIT HUP INT TERM
 kill_isolated
 systemctl reset-failed bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 if [ "$completed" -ne 1 ];then
  echo "Restore-and-compaction rehearsal did not complete; no compacted database was installed. Backup: $backup" >&2
 fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/memphis-before.json"
./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty >"$backup/nashville-before.json"
fetch_status "$backup/status-before.json"
guide_before=$(guide_snapshot)

systemctl stop bitcoinwalk-guide.service bitcoinwalk-replica-firstwalk.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-relay.service
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$memphis_db" "$backup/memphis-events.db"
cp -p "$nashville_db" "$backup/nashville-events.db"
cp -p "$source_target" "$backup/source-binary"
cp -p "$memphis_target" "$backup/memphis-binary"
cp -p "$nashville_target" "$backup/nashville-binary"
cp -p "$registry" "$backup/registry.json"
cp -p "$memphis_config" "$backup/receiver.conf"
cp -p "$nashville_config" "$backup/receiver-firstwalk.conf"
cp -p "$status_token" "$backup/replica-status-token"
cp -p "$guide_db" "$backup/guide.sqlite"
(
 cd "$backup"
 sha256sum source-events.db source-replication-journal.db memphis-events.db nashville-events.db source-binary memphis-binary nashville-binary registry.json receiver.conf receiver-firstwalk.conf replica-status-token guide.sqlite memphis-before.json nashville-before.json status-before.json >SHA256SUMS
)
echo "Consistent pre-compaction backup created: $backup"

systemctl start bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service bitcoinwalk-guide.service
wait_health 3334;wait_health 3341;wait_health 3342
test "$(systemctl is-active bitcoinwalk-relay.service)" = active
for unit in bitcoinwalk-guide bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk;do systemctl is-active --quiet "$unit";done
fetch_status "$backup/status-live-restored.json"
cmp "$backup/status-before.json" "$backup/status-live-restored.json"

compact "$backup/source-events.db" "$restore/source-events.db"
compact "$backup/source-replication-journal.db" "$restore/source-replication-journal.db"
compact "$backup/memphis-events.db" "$restore/memphis-events.db"
compact "$backup/nashville-events.db" "$restore/nashville-events.db"
digest_equal "$backup/source-events.db" "$restore/source-events.db"
digest_equal "$backup/source-replication-journal.db" "$restore/source-replication-journal.db"
digest_equal "$backup/memphis-events.db" "$restore/memphis-events.db"
digest_equal "$backup/nashville-events.db" "$restore/nashville-events.db"

env -i PATH=/usr/bin:/bin RELAY_LISTEN=127.0.0.1:3354 RELAY_DB="$restore/source-events.db" RELAY_ORGANIZER_MODE=true RELAY_REPLICA_JOURNAL="$restore/source-replication-journal.db" RELAY_REPLICA_REGISTRY="$backup/registry.json" RELAY_NAME='BitcoinWalk isolated compacted source recovery' "$maintenance" >"$backup/isolated-source.log" 2>&1 &
isolated_pids="$!"
env -i PATH=/usr/bin:/bin RELAY_LISTEN=127.0.0.1:3355 RELAY_DB="$restore/memphis-events.db" RELAY_ORGANIZER_MODE=true RELAY_NAME='BitcoinWalk isolated compacted Memphis recovery' "$maintenance" >"$backup/isolated-memphis.log" 2>&1 &
isolated_pids="$isolated_pids $!"
env -i PATH=/usr/bin:/bin RELAY_LISTEN=127.0.0.1:3356 RELAY_DB="$restore/nashville-events.db" RELAY_ORGANIZER_MODE=true RELAY_NAME='BitcoinWalk isolated compacted Nashville recovery' "$maintenance" >"$backup/isolated-nashville.log" 2>&1 &
isolated_pids="$isolated_pids $!"
wait_health 3354;wait_health 3355;wait_health 3356

./replica-audit-0.8.26 -source ws://127.0.0.1:3354 -replica ws://127.0.0.1:3355 >"$backup/memphis-restored.json"
./replica-audit-0.8.26 -source ws://127.0.0.1:3354 -city "$nashville" -replica ws://127.0.0.1:3356 -allow-empty >"$backup/nashville-restored.json"
audit_equal "$backup/memphis-before.json" "$backup/memphis-restored.json"
audit_equal "$backup/nashville-before.json" "$backup/nashville-restored.json"
kill_isolated

./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/memphis-after.json"
./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty >"$backup/nashville-after.json"
audit_equal "$backup/memphis-before.json" "$backup/memphis-after.json"
audit_equal "$backup/nashville-before.json" "$backup/nashville-after.json"
fetch_status "$backup/status-after.json"
cmp "$backup/status-before.json" "$backup/status-after.json"
test "$(guide_snapshot)" = "$guide_before"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done

(
 cd "$backup"
 sha256sum restore/source-events.db restore/source-replication-journal.db restore/memphis-events.db restore/nashville-events.db memphis-restored.json nashville-restored.json memphis-after.json nashville-after.json status-after.json >>SHA256SUMS
)
completed=1
trap - EXIT HUP INT TERM
echo "Isolated restore-and-compaction rehearsal accepted. Backup: $backup"
echo 'Memphis remained exact 7/7; cancellations were not resurrected. Nashville remained exact 0/0; revocation was not resurrected.'
echo 'No compacted database was installed into a live path.'
