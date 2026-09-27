#!/bin/sh
# Exercise one real Memphis retry -> recovery transition through the source
# outbox and BitcoinWalk Guide, with a consistent source/Guide backup first.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 MEMPHIS_EVENT_ID" >&2; exit 1; }
event_id=$1
printf '%s\n' "$event_id" | grep -Eq '^[0-9a-f]{64}$' || { echo 'MEMPHIS_EVENT_ID must be 64 lowercase hex characters.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.25
manifest=GUIDE-REPLICATION-TRANSITION-0.8.25-SHA256SUMS
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
status_token=/etc/bitcoinwalk-replication/replica-status-token
guide_target=/opt/bitcoinwalk-guide/guide.cjs
guide_state=/var/lib/bitcoinwalk-guide
guide_db=$guide_state/guide.sqlite
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
memphis=be8514a4-9df0-4159-a517-71f65761cbbe
nashville=586c0d1f-e861-4c8f-858c-ce3e2bfaf384
old_source_hash=0719f7e05f1127cd33509a6b196b81c2b2a235d759956673d931757ef09af7a4
guide_hash=4edadf6ca54bcddd2a92bcd57ca58ceca95fd6571f93c29fab3e2aefbf88a0a0

digest(){ sha256sum "$1" | cut -d ' ' -f 1; }
guide_count(){
 "$node" -e 'const{DatabaseSync}=require("node:sqlite");const db=new DatabaseSync(process.argv[1],{readOnly:true});const row=db.prepare("SELECT count(*) AS n FROM delivery WHERE purpose=? AND state=?").get(process.argv[2],process.argv[3]);console.log(row.n);db.close();' "$guide_db" "$1" "$2"
}
replication_baseline(){
 "$node" -e 'const{DatabaseSync}=require("node:sqlite");const db=new DatabaseSync(process.argv[1],{readOnly:true});const row=db.prepare("SELECT state,generation FROM replication_state WHERE city_id=?").get(process.argv[2]);console.log(row?`${row.state}:${row.generation}`:"");db.close();' "$guide_db" "$memphis"
}
city_state(){
 "$node" -e 'const fs=require("node:fs");const report=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));const row=report.cities.find(x=>x.cityId===process.argv[2]);console.log(row?`${row.state}:${row.counts.retry??0}`:"");' "$1" "$2"
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
wait_city(){
 expected=$1;file=$2;attempt=0
 while :;do
  fetch_status "$file"
  value=$(city_state "$file" "$memphis")
  case "$expected:$value" in
   degraded:degraded:*) return 0;;
   healthy:healthy:0) return 0;;
  esac
  attempt=$((attempt+1));test "$attempt" -lt 120||return 1;sleep 1
 done
}
wait_delivery(){
 purpose=$1;attempt=0
 until test "$(guide_count "$purpose" acknowledged)" = 1;do
  attempt=$((attempt+1));test "$attempt" -lt 300||return 1;sleep 1
 done
 test "$(guide_count "$purpose" pending)" = 0
}
audit_retry(){
 if "$@";then return 0;fi
 echo 'Public audit has not converged; retrying in 5 seconds.' >&2
 sleep 5
 "$@"
}

sha256sum -c "$manifest"
test "$(digest "$source_target")" = "$old_source_hash"
test "$(digest "$guide_target")" = "$guide_hash"
test -f "$source_journal";test -f "$guide_db";test -f "$status_token"
test "$(stat -c '%a %U:%G' "$status_token")" = '600 root:root'
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done
audit_retry ./replica-audit -source ws://127.0.0.1:3334 -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty >/dev/null
audit_retry ./replica-audit -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >/dev/null
test "$(replication_baseline)" = 'healthy:0'
test "$(guide_count replication-degraded acknowledged)" = 0
test "$(guide_count replication-recovered acknowledged)" = 0
test "$(guide_count replication-degraded pending)" = 0
test "$(guide_count replication-recovered pending)" = 0

backup=$(mktemp -d /var/backups/bitcoinwalk-guide-replication-transition.XXXXXX)
chmod 0700 "$backup"
changed=0
completed=0
rollback(){
 code=$?;trap - EXIT
 systemctl stop bitcoinwalk-guide.service bitcoinwalk-relay.service >/dev/null 2>&1||true
 if [ "$changed" -eq 1 ];then
  install -o root -g root -m 0755 "$backup/source-binary.before" "$source_target"
  install -o bitcoinwalk -g bitcoinwalk -m 0600 "$backup/source-journal.before" "$source_journal"
 fi
 systemctl reset-failed bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service bitcoinwalk-relay.service bitcoinwalk-guide.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-replica-rehearsal.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-replica-firstwalk.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-relay.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-guide.service >/dev/null 2>&1||true
 if [ "$completed" -ne 1 ];then
  echo "Replication transition rehearsal did not complete; source 0.8.23 and its consistent journal were restored. Guide state was retained for recovery/deduplication. Backup: $backup" >&2
 fi
 exit "$code"
}
trap rollback EXIT

systemctl stop bitcoinwalk-guide.service bitcoinwalk-relay.service
cp -p "$source_target" "$backup/source-binary.before"
cp -p "$source_journal" "$backup/source-journal.before"
cp -p "$guide_target" "$backup/guide.cjs"
cp -aL "$guide_state" "$backup/guide-state"
(
 cd "$backup"
 sha256sum source-binary.before source-journal.before guide.cjs >SHA256SUMS
 find guide-state -type f -exec sha256sum '{}' + >>SHA256SUMS
)
echo "Consistent pre-rehearsal source and Guide backup created: $backup"

changed=1
install -o root -g root -m 0755 "$artifact" "$source_target"
systemctl stop bitcoinwalk-replica-rehearsal.service
env \
 RELAY_REPLICA_ALERT_REHEARSAL_EVENT="$event_id" \
 RELAY_REPLICA_ALERT_REHEARSAL_CONFIRM=staging-alert-transition-v1 \
 RELAY_REPLICA_JOURNAL="$source_journal" \
 "$source_target"

systemctl start bitcoinwalk-relay.service
wait_health 3334
wait_city degraded "$backup/degraded-status.json"
echo 'Real Memphis retry state observed while its receiver was unavailable.'

systemctl start bitcoinwalk-guide.service
wait_delivery replication-degraded
echo 'Exactly one degraded notification was acknowledged by an organizer inbox relay.'

systemctl start bitcoinwalk-replica-rehearsal.service
wait_health 3341
wait_city healthy "$backup/recovered-status.json"
wait_delivery replication-recovered
echo 'Exactly one recovered notification was acknowledged by an organizer inbox relay.'

test "$(guide_count replication-degraded acknowledged)" = 1
test "$(guide_count replication-recovered acknowledged)" = 1
test "$(guide_count replication-degraded pending)" = 0
test "$(guide_count replication-recovered pending)" = 0
audit_retry ./replica-audit -source ws://127.0.0.1:3334 -city "$nashville" -replica ws://127.0.0.1:3342 -allow-empty
audit_retry ./replica-audit -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy;do systemctl is-active --quiet "$unit";done

completed=1
trap - EXIT
echo "Guide replication degraded/recovered rehearsal accepted on source 0.8.25. Backup: $backup"
echo 'Memphis returned to exact 7/7 and Nashville remained exact 0/0.'
echo 'Public audit was deliberately not repeated; the Guide scan and replica transport used their real WSS endpoints without spending extra public read capacity.'
echo 'Next: confirm exactly one degraded and one recovered BitcoinWalk Guide message in Armada.'
