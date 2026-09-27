#!/bin/sh
# Prove a real WSS delivery fails while the receiver is unavailable, then prove
# the exact acknowledged envelope is accepted idempotently after receiver restart.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 EVENT_ID" >&2; exit 1; }
event_id=$1
printf '%s\n' "$event_id" | grep -Eq '^[0-9a-f]{64}$' || { echo 'EVENT_ID must be 64 lowercase hex characters.' >&2; exit 1; }
cd "$(dirname "$0")/.."

source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
receiver_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
service_key=/etc/bitcoinwalk-replication/replica-delivery-key
expected_hash=7a74d9cdfc5d9609b60d6cd3f34dff99ae073565eaf3e56cfc6ce520752b5fa5

sha256sum -c REPLICA-RETRY-REHEARSAL-SHA256SUMS
test "$(sha256sum "$source_target" | cut -d ' ' -f 1)" = "$expected_hash" || { echo 'Live source is not the reviewed 0.8.17 binary.' >&2; exit 1; }
test "$(sha256sum "$receiver_target" | cut -d ' ' -f 1)" = "$expected_hash" || { echo 'Live receiver is not the reviewed 0.8.17 binary.' >&2; exit 1; }
test -f "$source_journal"
test "$(stat -c '%a %U:%G' "$service_key")" = '600 root:root'
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service
./replica-audit >/dev/null

completed=0
cleanup() {
 code=$?
 trap - EXIT
 systemctl start bitcoinwalk-replica-rehearsal.service >/dev/null 2>&1 || true
 systemctl start bitcoinwalk-relay.service >/dev/null 2>&1 || true
 if [ "$code" -ne 0 ] || [ "$completed" -ne 1 ]; then
  echo 'Controlled replica retry rehearsal failed; both services were started for recovery.' >&2
 fi
 exit "$code"
}
trap cleanup EXIT

systemctl stop bitcoinwalk-relay.service
systemctl stop bitcoinwalk-replica-rehearsal.service

echo 'Receiver stopped; expecting the first authenticated WSS delivery to fail.'
if env \
 RELAY_REPLICA_REPLAY_EVENT="$event_id" \
 RELAY_REPLICA_REPLAY_CONFIRM=staging-idempotence-v1 \
 RELAY_REPLICA_JOURNAL="$source_journal" \
 RELAY_REPLICA_DELIVERY_KEY_FILE="$service_key" \
 "$source_target"; then
 echo 'Replay unexpectedly succeeded while the receiver was stopped.' >&2
 exit 1
fi
echo 'Expected receiver-unavailable failure observed.'

systemctl start bitcoinwalk-replica-rehearsal.service
attempt=0
until curl --fail --silent --max-time 2 http://127.0.0.1:3341/healthz >/dev/null; do
 attempt=$((attempt+1)); test "$attempt" -lt 15 || exit 1; sleep 1
done

echo 'Receiver restored; replaying the exact acknowledged envelope.'
env \
 RELAY_REPLICA_REPLAY_EVENT="$event_id" \
 RELAY_REPLICA_REPLAY_CONFIRM=staging-idempotence-v1 \
 RELAY_REPLICA_JOURNAL="$source_journal" \
 RELAY_REPLICA_DELIVERY_KEY_FILE="$service_key" \
 "$source_target"

systemctl start bitcoinwalk-relay.service
attempt=0
until curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz >/dev/null; do
 attempt=$((attempt+1)); test "$attempt" -lt 15 || exit 1; sleep 1
done
./replica-audit
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service

completed=1
trap - EXIT
echo 'Controlled failure and exact idempotent retry rehearsal passed.'
