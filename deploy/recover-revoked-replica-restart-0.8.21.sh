#!/bin/sh
# Recover a source stopped by historical-approval reconciliation after a city
# revocation. Preserve all relay state; replace binaries only; prove a second
# source restart before acceptance.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.21
manifest=REPLICA-REVOKED-RESTART-0.8.21-SHA256SUMS
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
memphis_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
firstwalk_target=/opt/bitcoinwalk-replica-firstwalk/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
memphis_db=/var/lib/bitcoinwalk-replica-rehearsal/events.db
firstwalk_db=/var/lib/bitcoinwalk-replica-firstwalk/events.db
accepted_binary=416dbcac2341df863371e67c4e5664047b98c3be82e245d2b91ef2bdf8327f54
nashville=586c0d1f-e861-4c8f-858c-ce3e2bfaf384
nashville_replica=wss://replica-firstwalk-staging.bitcoinwalk.org/

audit_retry() {
 attempt=0
 until "$@"; do
  attempt=$((attempt+1)); test "$attempt" -lt 8 || return 1
  echo 'Public audit has not converged; retrying in 5 seconds.' >&2
  sleep 5
 done
}

wait_health() {
 for port in 3334 3341 3342; do
  attempt=0
  until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
   attempt=$((attempt+1)); test "$attempt" -lt 20 || return 1; sleep 1
  done
  curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "http://127.0.0.1:$port/" | grep -q 'bitcoinwalk-organizers-0.8.21'
 done
}

sha256sum -c "$manifest"
for target in "$source_target" "$memphis_target" "$firstwalk_target"; do
 test "$(sha256sum "$target" | cut -d ' ' -f 1)" = "$accepted_binary" || { echo "Unexpected live binary: $target" >&2; exit 1; }
done
systemctl is-failed --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet bitcoinwalk-replica-firstwalk.service
systemctl is-active --quiet caddy.service

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-revoked-restart.XXXXXX)
chmod 0700 "$backup"
changed=0
completed=0
rollback() {
 code=$?
 trap - EXIT
 if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
  systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service 2>/dev/null || true
  install -o root -g root -m 0755 "$backup/source-binary-0.8.20" "$source_target"
  install -o root -g root -m 0755 "$backup/memphis-binary-0.8.20" "$memphis_target"
  install -o root -g root -m 0755 "$backup/firstwalk-binary-0.8.20" "$firstwalk_target"
  systemctl start bitcoinwalk-replica-rehearsal.service >/dev/null 2>&1 || true
  systemctl start bitcoinwalk-replica-firstwalk.service >/dev/null 2>&1 || true
  systemctl start bitcoinwalk-relay.service >/dev/null 2>&1 || true
  echo "0.8.21 acceptance failed; 0.8.20 binaries restored without overwriting relay state. Backup: $backup" >&2
 fi
 if [ "$completed" -ne 1 ]; then echo 'Revoked-city restart recovery did not complete.' >&2; fi
 exit "$code"
}
trap rollback EXIT

systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service
cp -p "$source_target" "$backup/source-binary-0.8.20"
cp -p "$memphis_target" "$backup/memphis-binary-0.8.20"
cp -p "$firstwalk_target" "$backup/firstwalk-binary-0.8.20"
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$memphis_db" "$backup/memphis-events.db"
cp -p "$firstwalk_db" "$backup/firstwalk-events.db"
(
 cd "$backup"
 sha256sum source-binary-0.8.20 memphis-binary-0.8.20 firstwalk-binary-0.8.20 source-events.db source-replication-journal.db memphis-events.db firstwalk-events.db >SHA256SUMS
)
echo "Consistent pre-recovery backup created: $backup"

changed=1
install -o root -g root -m 0755 "$artifact" "$source_target"
install -o root -g root -m 0755 "$artifact" "$memphis_target"
install -o root -g root -m 0755 "$artifact" "$firstwalk_target"
systemctl reset-failed bitcoinwalk-relay.service
systemctl start bitcoinwalk-replica-rehearsal.service
systemctl start bitcoinwalk-replica-firstwalk.service
systemctl start bitcoinwalk-relay.service
wait_health
audit_retry ./replica-audit -city "$nashville" -replica "$nashville_replica" -allow-empty
audit_retry ./replica-audit >/dev/null

# Prove the formerly failing transition once more before accepting the release.
systemctl restart bitcoinwalk-relay.service
wait_health
audit_retry ./replica-audit -city "$nashville" -replica "$nashville_replica" -allow-empty
audit_retry ./replica-audit >/dev/null
for unit in bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy; do systemctl is-active --quiet "$unit"; done

completed=1
trap - EXIT
echo "Revoked-city restart recovery accepted on 0.8.21. Backup: $backup"
echo 'Nashville remained 0/0 and Memphis remained 7/7 across the repeated source restart.'
