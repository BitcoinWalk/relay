#!/bin/sh
# Replace the faulty shared-head backfill checkpoint logic and rebuild only the
# isolated replication journal/receiver database from authoritative source data.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.15
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
receiver_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
receiver_db=/var/lib/bitcoinwalk-replica-rehearsal/events.db

sha256sum -c REPLICA-BACKFILL-FIX-SHA256SUMS
test "$(sha256sum "$source_target" | cut -d ' ' -f 1)" = e4242c45089c5f72d8f6ae67013fb95cb3747297a75e516bbff5f9302d5faed1 || { echo 'Live source is not the reviewed 0.8.14 binary.' >&2; exit 1; }
test "$(sha256sum "$receiver_target" | cut -d ' ' -f 1)" = e4242c45089c5f72d8f6ae67013fb95cb3747297a75e516bbff5f9302d5faed1 || { echo 'Live receiver is not the reviewed 0.8.14 binary.' >&2; exit 1; }
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = e1540b17f14d1012fb94acdd50a71324a08dfe328f17d75ed32036b061133c5d || { echo 'Caddy changed after rehearsal activation; stop for review.' >&2; exit 1; }
test "$(sha256sum /etc/systemd/system/bitcoinwalk-relay.service.d/30-replication-source.conf | cut -d ' ' -f 1)" = f950a0bad2c5fb4eaab83d486bb8488e6fa3a2dcb840d3836bc7a9bb72e02d44 || { echo 'Source replication drop-in changed.' >&2; exit 1; }
test "$(sha256sum /etc/systemd/system/bitcoinwalk-replica-rehearsal.service | cut -d ' ' -f 1)" = e4a05636753866766d12af067b4d2af1840a5a20e9150a08a52baedde696d250 || { echo 'Receiver unit changed.' >&2; exit 1; }
test "$(stat -c '%a %U:%G' /etc/bitcoinwalk-replication)" = '755 root:root'
test "$(stat -c '%a %U:%G' /etc/bitcoinwalk-replication/replica-delivery-key)" = '600 root:root'
grep -Fqx '{"version":1,"cities":[{"cityId":"be8514a4-9df0-4159-a517-71f65761cbbe","destination":"wss://replica-staging.bitcoinwalk.org/"}]}' /etc/bitcoinwalk-replication/registry.json
grep -Fqx 'RELAY_REPLICA_RECEIVER_CITY=be8514a4-9df0-4159-a517-71f65761cbbe' /etc/bitcoinwalk-replication/receiver.conf
grep -Fqx 'RELAY_REPLICA_RECEIVER_DESTINATION=wss://replica-staging.bitcoinwalk.org/' /etc/bitcoinwalk-replication/receiver.conf
test -f "$source_journal"
test -f "$receiver_db"
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.14'
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3341/ | grep -q 'bitcoinwalk-organizers-0.8.14'

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-backfill-fix.XXXXXX)
chmod 0700 "$backup"
cp -p "$source_target" "$backup/source-binary-0.8.14"
cp -p "$receiver_target" "$backup/receiver-binary-0.8.14"
changed=0
journal_moved=0
receiver_moved=0
rollback() {
 code=$?
 trap - EXIT
 if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
  systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service 2>/dev/null || true
  install -o root -g root -m 0755 "$backup/source-binary-0.8.14" "$source_target"
  install -o root -g root -m 0755 "$backup/receiver-binary-0.8.14" "$receiver_target"
  if [ "$journal_moved" -eq 1 ]; then
   test ! -e "$source_journal" || mv "$source_journal" "$backup/failed-0.8.15-source-journal.db"
   mv "$backup/source-journal-0.8.14.db" "$source_journal"
  fi
  if [ "$receiver_moved" -eq 1 ]; then
   test ! -e "$receiver_db" || mv "$receiver_db" "$backup/failed-0.8.15-receiver.db"
   mv "$backup/receiver-0.8.14.db" "$receiver_db"
  fi
  systemctl start bitcoinwalk-replica-rehearsal.service || true
  systemctl start bitcoinwalk-relay.service || true
  echo "0.8.15 acceptance failed; 0.8.14 binaries and isolated state restored. Backup: $backup" >&2
 fi
 exit "$code"
}
trap rollback EXIT

systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service
changed=1
mv "$source_journal" "$backup/source-journal-0.8.14.db"
journal_moved=1
mv "$receiver_db" "$backup/receiver-0.8.14.db"
receiver_moved=1
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
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.15'
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3341/ | grep -q 'bitcoinwalk-organizers-0.8.15'

attempt=0
until ./replica-audit >/dev/null 2>&1; do
 attempt=$((attempt+1)); test "$attempt" -lt 15 || { ./replica-audit || true; exit 1; }; sleep 2
done
./replica-audit
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service

trap - EXIT
echo "Replication backfill checkpoint fix 0.8.15 accepted. Backup: $backup"
echo 'The old isolated journal and receiver database remain recoverable in the backup.'
