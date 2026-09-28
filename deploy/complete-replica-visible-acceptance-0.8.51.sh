#!/bin/sh
# Complete the seven-event candidate acceptance using the consistent backup
# already made by 0.8.50. The stable journal digest excludes only the volatile
# metadata/reconciled-at timestamp and still covers all operational records.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 2 || { echo "Usage: $0 /var/backups/bitcoinwalk-replica-visible-backfill.BACKUP /var/backups/bitcoinwalk-replica-visible-acceptance.BACKUP" >&2; exit 1; }
cd "$(dirname "$0")/.."

original=$1
acceptance=$2
case "$original" in /var/backups/bitcoinwalk-replica-visible-backfill.*) ;; *) echo 'Refusing an unexpected backfill path.' >&2;exit 1;; esac
case "$acceptance" in /var/backups/bitcoinwalk-replica-visible-acceptance.*) ;; *) echo 'Refusing an unexpected acceptance path.' >&2;exit 1;; esac
test ! -L "$original";test -d "$original";test ! -L "$acceptance";test -d "$acceptance"
for file in public-baseline.json backfill-result.json source-replication-journal.db registry.json directory-primary.before.json directory-secondary.before.json status.before.json;do test -f "$original/$file";done
for file in source-replication-journal.db registry.json guide.before.json source-events.db guide.sqlite source-binary;do test -f "$acceptance/$file";done

manifest=REPLICA-VISIBLE-ACCEPTANCE-0.8.51-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-stable-digest-0.8.51
audit=./replica-audit-shadow-0.8.44
capture=./deploy/capture-city-directory-root-0.8.35.mjs
registry=/etc/bitcoinwalk-replication/registry.json
guide_db=/var/lib/bitcoinwalk-guide/guide.sqlite
status_token=/etc/bitcoinwalk-replication/replica-status-token
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
candidate=wss://replica.bitcoinwalk.org/
directory_primary=wss://directory-staging.bitcoinwalk.org/
directory_secondary=wss://directory-2-staging.bitcoinwalk.org/
directory_root=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79

digest(){ sha256sum "$1" | cut -d ' ' -f 1; }
stable_digest(){ RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$1" "$artifact"; }
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
grep -q '"delivered":7' "$original/backfill-result.json"
grep -q '"journalMode":"read-only"' "$original/backfill-result.json"
test "$(digest "$original/registry.json")" = "$(digest "$acceptance/registry.json")"
test "$(digest "$acceptance/registry.json")" = "$(digest "$registry")"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-visible-acceptance-complete.XXXXXX)
chmod 0700 "$backup"
completed=0
finish(){
 code=$?;trap - EXIT HUP INT TERM
 if test "$completed" -ne 1;then echo "Stable visible-state acceptance did not complete; no state mutation was attempted. Evidence: $backup" >&2;fi
 exit "$code"
}
trap finish EXIT HUP INT TERM

stable_digest "$original/source-replication-journal.db" >"$backup/journal.before.digest"
stable_digest "$acceptance/source-replication-journal.db" >"$backup/journal.after.digest"
cmp "$backup/journal.before.digest" "$backup/journal.after.digest"

audit_retry "$audit" -source ws://127.0.0.1:3334 -replica "$candidate" >"$backup/candidate.accepted.json"
python3 - "$original/public-baseline.json" "$backup/candidate.accepted.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    baseline = json.load(stream)
with open(sys.argv[2], encoding="utf-8") as stream:
    candidate = json.load(stream)
expected = baseline["source"]["occurrenceIds"]
if baseline["cityId"] != candidate["cityId"] or candidate["source"]["occurrenceIds"] != expected or candidate["replica"]["occurrenceIds"] != expected:
    raise SystemExit("candidate differs from the exact accepted public baseline")
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
guide_snapshot >"$backup/guide.accepted.json"
cmp "$acceptance/guide.before.json" "$backup/guide.accepted.json"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done
(
 cd "$backup"
 sha256sum journal.before.digest journal.after.digest candidate.accepted.json live-replica.accepted.json directory-primary.accepted.json directory-secondary.accepted.json status.accepted.json guide.accepted.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Exact visible-state Memphis backfill accepted on 0.8.51. Evidence: $backup"
echo 'Candidate and existing receiver are exact 7/7; seven durable public envelopes were delivered and the hidden historical row remained excluded.'
echo 'The stable source-journal digest, registry, signed directory chain, replication status and Guide delivery state remained unchanged.'
echo 'Only metadata/reconciled-at changed across the required source restart; no operational journal record changed.'
echo 'The candidate is still not a live registry or signed-directory endpoint.'
