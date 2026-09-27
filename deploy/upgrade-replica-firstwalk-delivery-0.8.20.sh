#!/bin/sh
# Recover a first walk whose approval released it publicly but whose pre-approval
# journal eligibility was not persisted. Startup reconciliation on 0.8.20
# repairs the stale outbox row and retries the exact signed event.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.20
manifest=REPLICA-FIRSTWALK-DELIVERY-0.8.20-SHA256SUMS
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
memphis_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
firstwalk_target=/opt/bitcoinwalk-replica-firstwalk/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
memphis_db=/var/lib/bitcoinwalk-replica-rehearsal/events.db
firstwalk_db=/var/lib/bitcoinwalk-replica-firstwalk/events.db
city_id=586c0d1f-e861-4c8f-858c-ce3e2bfaf384
destination=wss://replica-firstwalk-staging.bitcoinwalk.org/

accepted_binary=b68b5b9190499fd0acf80cbd907364f3f557d9b6c77d2bebf471da7bf0dcc071
accepted_registry=1241d159014a29498b24fc46bb279931aa37347413e6740cf04a323d62bf3604
accepted_caddy=4c994fa4c1d603bb9eb22d9ee1c3720812887ac562a3a51c30b14477033eeb2d
accepted_memphis_config=a758554fbf460a90e7f4b179fa2589366fd220505604856b47b114233436cfce
accepted_firstwalk_config=a0dd26a209974a3a6279560469eab229046d471a27b4829a083dad613ff6dac3
accepted_dropin=f950a0bad2c5fb4eaab83d486bb8488e6fa3a2dcb840d3836bc7a9bb72e02d44
accepted_firstwalk_unit=888a4f17b935f9b5f1e2ad5af5a339544616b678f45c7c9394cb3b7020ae7ec3

run_public_audit() {
 attempt=0
 until "$@"; do
  attempt=$((attempt+1))
  test "$attempt" -lt 8 || return 1
  echo 'Public audit has not converged yet; retrying in 5 seconds.' >&2
  sleep 5
 done
}

sha256sum -c "$manifest"
for target in "$source_target" "$memphis_target" "$firstwalk_target"; do
 test "$(sha256sum "$target" | cut -d ' ' -f 1)" = "$accepted_binary" || { echo "Unexpected live binary: $target" >&2; exit 1; }
done
test "$(sha256sum /etc/bitcoinwalk-replication/registry.json | cut -d ' ' -f 1)" = "$accepted_registry"
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = "$accepted_caddy"
test "$(sha256sum /etc/bitcoinwalk-replication/receiver.conf | cut -d ' ' -f 1)" = "$accepted_memphis_config"
test "$(sha256sum /etc/bitcoinwalk-replication/receiver-firstwalk.conf | cut -d ' ' -f 1)" = "$accepted_firstwalk_config"
test "$(sha256sum /etc/systemd/system/bitcoinwalk-relay.service.d/30-replication-source.conf | cut -d ' ' -f 1)" = "$accepted_dropin"
test "$(sha256sum /etc/systemd/system/bitcoinwalk-replica-firstwalk.service | cut -d ' ' -f 1)" = "$accepted_firstwalk_unit"
for unit in bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy; do systemctl is-active --quiet "$unit"; done

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-firstwalk-delivery.XXXXXX)
chmod 0700 "$backup"
changed=0
completed=0
rollback() {
 code=$?
 trap - EXIT
 if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
  systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service 2>/dev/null || true
  install -o root -g root -m 0755 "$backup/source-binary-0.8.19" "$source_target"
  install -o root -g root -m 0755 "$backup/memphis-binary-0.8.19" "$memphis_target"
  install -o root -g root -m 0755 "$backup/firstwalk-binary-0.8.19" "$firstwalk_target"
  systemctl start bitcoinwalk-replica-rehearsal.service >/dev/null 2>&1 || true
  systemctl start bitcoinwalk-replica-firstwalk.service >/dev/null 2>&1 || true
  systemctl start bitcoinwalk-relay.service >/dev/null 2>&1 || true
  echo "0.8.20 acceptance failed; 0.8.19 binaries restored without overwriting newer relay state. Backup: $backup" >&2
 fi
 if [ "$completed" -ne 1 ]; then
  echo 'First-walk delivery recovery did not complete.' >&2
 fi
 exit "$code"
}
trap rollback EXIT

systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service
cp -p "$source_target" "$backup/source-binary-0.8.19"
cp -p "$memphis_target" "$backup/memphis-binary-0.8.19"
cp -p "$firstwalk_target" "$backup/firstwalk-binary-0.8.19"
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$memphis_db" "$backup/memphis-events.db"
cp -p "$firstwalk_db" "$backup/firstwalk-events.db"
cp -p /etc/bitcoinwalk-replication/registry.json "$backup/registry.json"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile"
(
 cd "$backup"
 sha256sum source-binary-0.8.19 memphis-binary-0.8.19 firstwalk-binary-0.8.19 source-events.db source-replication-journal.db memphis-events.db firstwalk-events.db registry.json Caddyfile >SHA256SUMS
)
echo "Consistent pre-recovery backup created: $backup"

changed=1
install -o root -g root -m 0755 "$artifact" "$source_target"
install -o root -g root -m 0755 "$artifact" "$memphis_target"
install -o root -g root -m 0755 "$artifact" "$firstwalk_target"
systemctl start bitcoinwalk-replica-rehearsal.service
systemctl start bitcoinwalk-replica-firstwalk.service
systemctl start bitcoinwalk-relay.service

for port in 3334 3341 3342; do
 attempt=0
 until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 20 || exit 1; sleep 1
 done
 curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "http://127.0.0.1:$port/" | grep -q 'bitcoinwalk-organizers-0.8.20'
done

run_public_audit ./replica-audit -city "$city_id" -replica "$destination"
run_public_audit ./replica-audit >/dev/null
for unit in bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy; do systemctl is-active --quiet "$unit"; done

completed=1
trap - EXIT
echo "Exact first-walk delivery recovered on 0.8.20. Backup: $backup"
echo 'Nashville source/receiver equality and Memphis source/receiver equality both passed.'
