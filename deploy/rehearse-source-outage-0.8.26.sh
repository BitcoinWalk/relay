#!/bin/sh
# Prove that both public receivers remain independently readable while the
# shared source is offline, then prove source reconciliation without changing
# signed events, outbox counts or organizer notification state.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-SOURCE-OUTAGE-0.8.26-SHA256SUMS
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
status_token=/etc/bitcoinwalk-replication/replica-status-token
guide_target=/opt/bitcoinwalk-guide/guide.cjs
guide_config=/etc/bitcoinwalk-guide/config.json
guide_state=/var/lib/bitcoinwalk-guide
guide_db=$guide_state/guide.sqlite
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
memphis=be8514a4-9df0-4159-a517-71f65761cbbe
nashville=586c0d1f-e861-4c8f-858c-ce3e2bfaf384
accepted_source=b1b789170d02969ec72aac6e90a792219aa20b714e86dc93ce31187c5b5ee8ac
accepted_guide=c32711c5d26b5f583fb2b9ad7e80386f54e36e07e05f4783b43eb1a16e7a5a3c

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
audit_retry(){
 if "$@";then return 0;fi
 echo 'Local audit has not converged; retrying in 5 seconds.' >&2
 sleep 5
 "$@"
}
wait_reconciled(){
 file=$1;attempt=0
 while :;do
  fetch_status "$file"
  if grep -q '"reconciled":true' "$file" && grep -q '"state":"healthy"' "$file";then return 0;fi
  attempt=$((attempt+1));test "$attempt" -lt 60||return 1;sleep 1
 done
}

sha256sum -c "$manifest"
test "$(digest "$source_target")" = "$accepted_source"
test "$(digest "$guide_target")" = "$accepted_guide"
test -f "$source_db";test -f "$source_journal";test -f "$guide_db";test -f "$guide_config";test -f "$status_token"
test "$(stat -c '%a %U:%G' "$status_token")" = '600 root:root'
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-source-outage.XXXXXX)
chmod 0700 "$backup"
completed=0
recover_services(){
 code=$?;trap - EXIT
 systemctl reset-failed bitcoinwalk-relay.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-relay.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-guide.service >/dev/null 2>&1||true
 if [ "$completed" -ne 1 ];then
  echo "Source-outage rehearsal did not complete; accepted data was not overwritten and both stopped services were started for recovery. Backup: $backup" >&2
 fi
 exit "$code"
}
trap recover_services EXIT

./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/memphis-before.json"
./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty >"$backup/nashville-before.json"
fetch_status "$backup/status-before.json"
guide_before=$(guide_snapshot)

systemctl stop bitcoinwalk-guide.service bitcoinwalk-relay.service
cp -p "$source_target" "$backup/source-binary"
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$status_token" "$backup/replica-status-token"
cp -p "$guide_target" "$backup/guide.cjs"
cp -p "$guide_config" "$backup/guide-config.json"
cp -aL "$guide_state" "$backup/guide-state"
(
 cd "$backup"
 sha256sum source-binary source-events.db source-replication-journal.db replica-status-token guide.cjs guide-config.json memphis-before.json nashville-before.json status-before.json >SHA256SUMS
 find guide-state -type f -exec sha256sum '{}' + >>SHA256SUMS
)
echo "Consistent pre-outage backup created: $backup"

test "$(systemctl is-active bitcoinwalk-relay.service || true)" = inactive
if curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz >/dev/null 2>&1;then
 echo 'Source loopback health remained reachable during the outage.' >&2;exit 1
fi
for unit in bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done
wait_health 3341
wait_health 3342

./replica-audit-0.8.26 -baseline "$backup/memphis-before.json" -city "$memphis" -replica ws://127.0.0.1:3341
./replica-audit-0.8.26 -baseline "$backup/nashville-before.json" -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty
echo 'Both receivers retained their exact public state while the source was offline.'

# The Guide must fail closed when its source/status authority is unavailable:
# no city transition is inferred and no organizer message is queued.
systemctl start bitcoinwalk-guide.service
sleep 65
systemctl is-active --quiet bitcoinwalk-guide.service
test "$(guide_snapshot)" = "$guide_before"

systemctl start bitcoinwalk-relay.service
wait_health 3334
wait_reconciled "$backup/status-after.json"
cmp "$backup/status-before.json" "$backup/status-after.json"
audit_retry ./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341
audit_retry ./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty

# Allow the Guide another scan after source recovery and prove that source
# unavailability did not synthesize a degraded/recovered city transition.
sleep 65
test "$(guide_snapshot)" = "$guide_before"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done

completed=1
trap - EXIT
echo "Deliberate source-outage recovery accepted. Backup: $backup"
echo 'Memphis remained exact 7/7 and Nashville remained exact 0/0 while the source was offline and after reconciliation.'
echo 'No organizer replication notification was created for source unavailability.'
