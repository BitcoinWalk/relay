#!/bin/sh
# Resume acceptance after the exact seven-event seed completed but the 0.8.49
# verifier attempted to open the live, locked Bolt journal. This is read-only:
# it does not redeliver, reset, or change registry/directory configuration.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-replica-visible-backfill.BACKUP" >&2; exit 1; }
cd "$(dirname "$0")/.."

original=$1
case "$original" in /var/backups/bitcoinwalk-replica-visible-backfill.*) ;; *) echo 'Refusing an unexpected backfill path.' >&2;exit 1;; esac
test ! -L "$original";test -d "$original"
for file in public-baseline.json candidate.before.json candidate.after.json backfill-result.json source-replication-journal.db registry.json source-binary directory-primary.before.json directory-secondary.before.json status.before.json;do test -f "$original/$file";done

manifest=REPLICA-VISIBLE-ACCEPTANCE-0.8.50-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-shadow-0.8.49
audit=./replica-audit-shadow-0.8.44
capture=./deploy/capture-city-directory-root-0.8.35.mjs
source_binary=/opt/bitcoinwalk-relay/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
registry=/etc/bitcoinwalk-replication/registry.json
guide_db=/var/lib/bitcoinwalk-guide/guide.sqlite
status_token=/etc/bitcoinwalk-replication/replica-status-token
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
city=be8514a4-9df0-4159-a517-71f65761cbbe
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
test "$(digest "$original/source-binary")" = "$accepted_source"
grep -q '"delivered":7' "$original/backfill-result.json"
grep -q '"journalMode":"read-only"' "$original/backfill-result.json"
for file in "$source_db" "$source_journal" "$registry" "$guide_db" "$status_token";do test -f "$file";done
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-visible-acceptance.XXXXXX)
chmod 0700 "$backup"
completed=0
recover(){
 code=$?;trap - EXIT HUP INT TERM
 systemctl reset-failed bitcoinwalk-relay.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-relay.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 if test "$completed" -ne 1;then echo "Visible-state acceptance did not complete; no candidate delivery, registry or directory mutation was attempted. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

guide_snapshot >"$backup/guide.before.json"
systemctl stop bitcoinwalk-guide.service bitcoinwalk-relay.service
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$guide_db" "$backup/guide.sqlite"
cp -p "$registry" "$backup/registry.json"
cp -p "$source_binary" "$backup/source-binary"
sync "$backup/source-events.db" "$backup/source-replication-journal.db" "$backup/guide.sqlite" "$backup/registry.json" "$backup/source-binary"
echo "Consistent acceptance backup created: $backup"
systemctl start bitcoinwalk-relay.service bitcoinwalk-guide.service
wait_source_health

test "$(digest "$original/registry.json")" = "$(digest "$backup/registry.json")"
test "$(digest "$backup/registry.json")" = "$(digest "$registry")"
test "$(bolt_digest "$original/source-replication-journal.db")" = "$(bolt_digest "$backup/source-replication-journal.db")"

audit_retry "$audit" -source ws://127.0.0.1:3334 -replica "$candidate" >"$backup/candidate.accepted.json"
python3 - "$original/public-baseline.json" "$backup/candidate.accepted.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    baseline = json.load(stream)
with open(sys.argv[2], encoding="utf-8") as stream:
    candidate = json.load(stream)
if baseline["cityId"] != candidate["cityId"]:
    raise SystemExit("candidate city differs from the accepted baseline")
expected = baseline["source"]["occurrenceIds"]
if candidate["source"]["occurrenceIds"] != expected or candidate["replica"]["occurrenceIds"] != expected:
    raise SystemExit("candidate event IDs differ from the accepted public baseline")
if not candidate["readOnly"] or not candidate["exactEventIds"]:
    raise SystemExit("candidate acceptance is not exact and read-only")
for side in ("source", "replica"):
    if not candidate[side]["allSignaturesValid"] or candidate[side]["privateWrapperCount"] != 0:
        raise SystemExit("candidate signature or privacy acceptance failed")
PY
audit_retry "$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/live-replica.accepted.json"
cmp "$original/public-baseline.json" "$backup/live-replica.accepted.json"

"$node" "$capture" "$directory_primary" "$directory_root" >"$backup/directory-primary.accepted.json"
"$node" "$capture" "$directory_secondary" "$directory_root" >"$backup/directory-secondary.accepted.json"
cmp "$original/directory-primary.before.json" "$backup/directory-primary.accepted.json"
cmp "$original/directory-secondary.before.json" "$backup/directory-secondary.accepted.json"
fetch_status "$backup/status.accepted.json"
cmp "$original/status.before.json" "$backup/status.accepted.json"
guide_snapshot >"$backup/guide.after.json"
cmp "$backup/guide.before.json" "$backup/guide.after.json"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done
(
 cd "$backup"
 sha256sum source-events.db source-replication-journal.db guide.sqlite registry.json source-binary guide.before.json guide.after.json candidate.accepted.json live-replica.accepted.json directory-primary.accepted.json directory-secondary.accepted.json status.accepted.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Exact visible-state Memphis backfill acceptance resumed on 0.8.50. Backup: $backup"
echo 'Candidate and existing receiver are exact 7/7; seven durable public envelopes were delivered and the hidden historical row remained excluded.'
echo 'The source journal logical digest, registry, signed directory chain, replication status and Guide delivery state remained unchanged.'
echo 'The candidate is still not a live registry or signed-directory endpoint.'
