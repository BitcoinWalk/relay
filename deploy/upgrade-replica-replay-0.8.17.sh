#!/bin/sh
# Install the read-only, acknowledged-envelope replay mode used by the
# controlled staging retry rehearsal. Replication state is preserved.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.17
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
receiver_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay

sha256sum -c REPLICA-RETRY-REHEARSAL-SHA256SUMS
test "$(sha256sum "$source_target" | cut -d ' ' -f 1)" = 1e68a5ede895a0efdc7fcd54648ccaa55046ad0a943650e7f79bde948a072fc2 || { echo 'Live source is not the accepted 0.8.16 binary.' >&2; exit 1; }
test "$(sha256sum "$receiver_target" | cut -d ' ' -f 1)" = 1e68a5ede895a0efdc7fcd54648ccaa55046ad0a943650e7f79bde948a072fc2 || { echo 'Live receiver is not the accepted 0.8.16 binary.' >&2; exit 1; }
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.16'
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3341/ | grep -q 'bitcoinwalk-organizers-0.8.16'
./replica-audit >/dev/null

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-replay.XXXXXX)
chmod 0700 "$backup"
cp -p "$source_target" "$backup/source-binary-0.8.16"
cp -p "$receiver_target" "$backup/receiver-binary-0.8.16"
changed=0
rollback() {
 code=$?
 trap - EXIT
 if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
  systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service 2>/dev/null || true
  install -o root -g root -m 0755 "$backup/source-binary-0.8.16" "$source_target"
  install -o root -g root -m 0755 "$backup/receiver-binary-0.8.16" "$receiver_target"
  systemctl start bitcoinwalk-replica-rehearsal.service || true
  systemctl start bitcoinwalk-relay.service || true
  echo "0.8.17 acceptance failed; 0.8.16 binaries restored. Backup: $backup" >&2
 fi
 exit "$code"
}
trap rollback EXIT

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
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.17'
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3341/ | grep -q 'bitcoinwalk-organizers-0.8.17'
./replica-audit
systemctl is-active --quiet caddy.service

trap - EXIT
echo "Read-only replica replay mode 0.8.17 accepted. Backup: $backup"
echo 'Replication journal and receiver database were preserved unchanged.'
