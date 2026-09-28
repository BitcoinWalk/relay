#!/bin/sh
# Backfill the isolated Memphis candidate from a consistent read-only copy of
# the source journal. The live registry and signed directory chain are never
# used as write targets by this workflow.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-SHADOW-BACKFILL-0.8.44-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-shadow-0.8.44
audit=./replica-audit-shadow-0.8.44
capture=./deploy/capture-city-directory-root-0.8.35.mjs
source_binary=/opt/bitcoinwalk-relay/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
registry=/etc/bitcoinwalk-replication/registry.json
delivery_key=/etc/bitcoinwalk-replication/replica-delivery-key
status_token=/etc/bitcoinwalk-replication/replica-status-token
guide_db=/var/lib/bitcoinwalk-guide/guide.sqlite
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
city=be8514a4-9df0-4159-a517-71f65761cbbe
source_destination=wss://replica-staging.bitcoinwalk.org/
candidate=wss://replica.bitcoinwalk.org/
directory_primary=wss://directory-staging.bitcoinwalk.org/
directory_secondary=wss://directory-2-staging.bitcoinwalk.org/
directory_root=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
accepted_source=b1b789170d02969ec72aac6e90a792219aa20b714e86dc93ce31187c5b5ee8ac

digest(){ sha256sum "$1" | cut -d ' ' -f 1; }
bolt_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }
wait_source_health(){
 attempt=0
 until curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz >/dev/null;do
  attempt=$((attempt+1));test "$attempt" -lt 30||return 1;sleep 1
 done
}
audit_retry(){
 attempt=0
 until "$@";do
  attempt=$((attempt+1));test "$attempt" -lt 6||return 1
  echo 'Public audit has not converged; retrying in 5 seconds.' >&2
  sleep 5
 done
}
guide_snapshot(){
 "$node" -e 'const{DatabaseSync}=require("node:sqlite");const db=new DatabaseSync(process.argv[1],{readOnly:true});console.log(JSON.stringify(db.prepare("SELECT purpose,state,count(*) AS count FROM delivery GROUP BY purpose,state ORDER BY purpose,state").all()));db.close();' "$guide_db"
}
fetch_status(){
 curl --fail --silent --show-error --max-time 5 -H "Authorization: Bearer $(tr -d '\n' <"$status_token")" http://127.0.0.1:3334/replication/status >"$1"
}

sha256sum -c "$manifest"
test "$(digest "$source_binary")" = "$accepted_source"
for file in "$source_db" "$source_journal" "$registry" "$delivery_key" "$status_token" "$guide_db";do test -f "$file";done
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-shadow-backfill.XXXXXX)
chmod 0700 "$backup"
completed=0
recover(){
 code=$?;trap - EXIT HUP INT TERM
 systemctl reset-failed bitcoinwalk-relay.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-relay.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 if test "$completed" -ne 1;then
  echo "Shadow backfill did not complete; live registry and directory chain were not changed. The isolated candidate may contain an acknowledged prefix and is safe to retry. Backup: $backup" >&2
 fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

audit_retry "$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/live-replica.before.json"
audit_retry "$audit" -source "$candidate" -replica "$candidate" -city "$city" -allow-empty >"$backup/candidate.before.json"
"$node" "$capture" "$directory_primary" "$directory_root" >"$backup/directory-primary.before.json"
"$node" "$capture" "$directory_secondary" "$directory_root" >"$backup/directory-secondary.before.json"
cmp "$backup/directory-primary.before.json" "$backup/directory-secondary.before.json"
fetch_status "$backup/status.before.json"
guide_before=$(guide_snapshot)

systemctl stop bitcoinwalk-guide.service bitcoinwalk-relay.service
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$registry" "$backup/registry.json"
cp -p "$source_binary" "$backup/source-binary"
sync "$backup/source-replication-journal.db" "$backup/registry.json" "$backup/source-binary"
echo "Consistent pre-backfill source backup created: $backup"
systemctl start bitcoinwalk-relay.service bitcoinwalk-guide.service
wait_source_health

registry_before=$(digest "$backup/registry.json")
journal_before=$(bolt_digest "$backup/source-replication-journal.db")
env -i PATH=/usr/bin:/bin \
 RELAY_REPLICA_JOURNAL="$backup/source-replication-journal.db" \
 RELAY_REPLICA_DELIVERY_KEY_FILE="$delivery_key" \
 RELAY_REPLICA_SHADOW_CITY="$city" \
 RELAY_REPLICA_SHADOW_SOURCE="$source_destination" \
 RELAY_REPLICA_SHADOW_CANDIDATE="$candidate" \
 RELAY_REPLICA_SHADOW_CONFIRM=production-candidate-shadow-v1 \
 "$artifact" >"$backup/backfill-result.json"
grep -q '"delivered":8' "$backup/backfill-result.json"
grep -q '"journalMode":"read-only"' "$backup/backfill-result.json"

audit_retry "$audit" -source ws://127.0.0.1:3334 -replica "$candidate" >"$backup/candidate.after.json"
audit_retry "$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/live-replica.after.json"
cmp "$backup/live-replica.before.json" "$backup/live-replica.after.json"
"$node" "$capture" "$directory_primary" "$directory_root" >"$backup/directory-primary.after.json"
"$node" "$capture" "$directory_secondary" "$directory_root" >"$backup/directory-secondary.after.json"
cmp "$backup/directory-primary.before.json" "$backup/directory-primary.after.json"
cmp "$backup/directory-secondary.before.json" "$backup/directory-secondary.after.json"
test "$registry_before" = "$(digest "$registry")"
test "$journal_before" = "$(bolt_digest "$source_journal")"
fetch_status "$backup/status.after.json"
cmp "$backup/status.before.json" "$backup/status.after.json"
test "$guide_before" = "$(guide_snapshot)"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done
(
 cd "$backup"
 sha256sum source-replication-journal.db registry.json source-binary backfill-result.json live-replica.before.json live-replica.after.json candidate.before.json candidate.after.json directory-primary.before.json directory-primary.after.json directory-secondary.before.json directory-secondary.after.json status.before.json status.after.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Memphis shadow backfill accepted on 0.8.44. Backup: $backup"
echo 'Candidate reached exact source equality at 7/7; the existing Memphis receiver remained exact at 7/7.'
echo 'The live registry, source journal, signed directory chain and Guide delivery state remained unchanged.'
echo 'The candidate is still not a live registry or signed-directory endpoint.'
