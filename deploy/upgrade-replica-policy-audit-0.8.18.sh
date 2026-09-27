#!/bin/sh
# Install the fail-closed public receiver policy audit and prove that rejected
# deliveries leave the receiver database and public Memphis baseline unchanged.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 ACKNOWLEDGED_EVENT_ID" >&2; exit 1; }
event_id=$1
printf '%s\n' "$event_id" | grep -Eq '^[0-9a-f]{64}$' || { echo 'ACKNOWLEDGED_EVENT_ID must be 64 lowercase hex characters.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.18
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
receiver_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
receiver_db=/var/lib/bitcoinwalk-replica-rehearsal/events.db
service_key=/etc/bitcoinwalk-replication/replica-delivery-key
accepted_hash=7a74d9cdfc5d9609b60d6cd3f34dff99ae073565eaf3e56cfc6ce520752b5fa5

run_public_audit() {
 attempt=0
 until ./replica-audit; do
  attempt=$((attempt+1))
  test "$attempt" -lt 6 || return 1
  echo 'Public audit was rate-limited or temporarily unavailable; retrying in 5 seconds.' >&2
  sleep 5
 done
}

sha256sum -c REPLICA-POLICY-AUDIT-SHA256SUMS
test "$(sha256sum "$source_target" | cut -d ' ' -f 1)" = "$accepted_hash" || { echo 'Live source is not the accepted 0.8.17 binary.' >&2; exit 1; }
test "$(sha256sum "$receiver_target" | cut -d ' ' -f 1)" = "$accepted_hash" || { echo 'Live receiver is not the accepted 0.8.17 binary.' >&2; exit 1; }
test -f "$source_db"
test -f "$source_journal"
test -f "$receiver_db"
test "$(stat -c '%a %U:%G' "$service_key")" = '600 root:root'
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-policy-audit.XXXXXX)
chmod 0700 "$backup"
changed=0
completed=0
rollback() {
 code=$?
 trap - EXIT
 systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service 2>/dev/null || true
 if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
  install -o root -g root -m 0755 "$backup/source-binary-0.8.17" "$source_target"
  install -o root -g root -m 0755 "$backup/receiver-binary-0.8.17" "$receiver_target"
  echo "0.8.18 acceptance failed; 0.8.17 binaries restored. Backup: $backup" >&2
 fi
 systemctl start bitcoinwalk-replica-rehearsal.service >/dev/null 2>&1 || true
 systemctl start bitcoinwalk-relay.service >/dev/null 2>&1 || true
 if [ "$completed" -ne 1 ]; then
  echo 'Policy rehearsal did not complete; both services were started for recovery.' >&2
 fi
 exit "$code"
}
trap rollback EXIT

systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service
cp -p "$source_target" "$backup/source-binary-0.8.17"
cp -p "$receiver_target" "$backup/receiver-binary-0.8.17"
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$receiver_db" "$backup/receiver-events.db"
cp -p /etc/bitcoinwalk-replication/registry.json "$backup/registry.json"
cp -p /etc/bitcoinwalk-replication/receiver.conf "$backup/receiver.conf"
cp -p "$service_key" "$backup/replica-delivery-key"
cp -p /etc/systemd/system/bitcoinwalk-relay.service.d/30-replication-source.conf "$backup/30-replication-source.conf"
cp -p /etc/systemd/system/bitcoinwalk-replica-rehearsal.service "$backup/bitcoinwalk-replica-rehearsal.service"
(
 cd "$backup"
 sha256sum source-binary-0.8.17 receiver-binary-0.8.17 source-events.db source-replication-journal.db receiver-events.db registry.json receiver.conf replica-delivery-key 30-replication-source.conf bitcoinwalk-replica-rehearsal.service >SHA256SUMS
)
echo "Consistent pre-upgrade backup created: $backup"
systemctl start bitcoinwalk-replica-rehearsal.service
systemctl start bitcoinwalk-relay.service
run_public_audit >/dev/null
systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service

changed=1
install -o root -g root -m 0755 "$artifact" "$source_target"
install -o root -g root -m 0755 "$artifact" "$receiver_target"
systemctl start bitcoinwalk-replica-rehearsal.service
systemctl start bitcoinwalk-relay.service

for port in 3334 3341; do
 attempt=0
 until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 15 || exit 1; sleep 1
 done
done
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.18'
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3341/ | grep -q 'bitcoinwalk-organizers-0.8.18'
run_public_audit >/dev/null

systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service
receiver_before=$(RELAY_REPLICA_DB_DIGEST="$receiver_db" "./$artifact")
systemctl start bitcoinwalk-replica-rehearsal.service
attempt=0
until curl --fail --silent --max-time 2 http://127.0.0.1:3341/healthz >/dev/null; do
 attempt=$((attempt+1)); test "$attempt" -lt 15 || exit 1; sleep 1
done

env \
 RELAY_REPLICA_POLICY_AUDIT_EVENT="$event_id" \
 RELAY_REPLICA_POLICY_AUDIT_CONFIRM=staging-policy-v1 \
 RELAY_REPLICA_JOURNAL="$source_journal" \
 RELAY_REPLICA_DELIVERY_KEY_FILE="$service_key" \
 "$source_target"

systemctl stop bitcoinwalk-replica-rehearsal.service
receiver_after=$(RELAY_REPLICA_DB_DIGEST="$receiver_db" "./$artifact")
test "$receiver_before" = "$receiver_after" || { echo 'Rejected policy probes changed logical receiver state.' >&2; exit 1; }
systemctl start bitcoinwalk-replica-rehearsal.service
systemctl start bitcoinwalk-relay.service

for port in 3334 3341; do
 attempt=0
 until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 15 || exit 1; sleep 1
 done
done
run_public_audit
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service

completed=1
trap - EXIT
echo "Public receiver negative-policy rehearsal passed. Backup: $backup"
echo 'Unauthorized service, foreign city and unauthorized author were rejected before receiver state changed.'
