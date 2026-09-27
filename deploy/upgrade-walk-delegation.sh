#!/bin/sh
# Staging-only single-walk delegation policy. Retires mistaken city-wide invitation access.
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

binary=bitcoinwalk-relay-walk-delegation-0.7.1
target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay/events.db
test -f "$binary"
printf '%s  %s\n' '980dc54026ba3a7177b7ace16a0efe26070ef9ad3b8eb2707c7e0ba4a36d3a58' "$binary" | sha256sum -c -
systemctl is-active --quiet bitcoinwalk-relay
printf '%s  %s\n' 'ab60256893d89a147c0f5dc378914a6fad277a45c3534c34377970d697de819f' "$target" | sha256sum -c -
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.7.0'
test -f "$database"

backup=$(mktemp -d /var/backups/bitcoinwalk-walk-delegation.XXXXXX)
chmod 0700 "$backup"
cp -p "$target" "$backup/bitcoinwalk-relay"
changed=0
rollback() {
  code=$?
  trap - EXIT
  if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
    systemctl stop bitcoinwalk-relay || true
    install -m 0755 -o root -g root "$backup/bitcoinwalk-relay" "$target"
    systemctl start bitcoinwalk-relay || true
    echo "Relay upgrade failed; previous binary restoration attempted. Database retained. Backup: $backup" >&2
  fi
  exit "$code"
}
trap rollback EXIT

systemctl stop bitcoinwalk-relay
changed=1
cp -p "$database" "$backup/events.db"
install -m 0755 -o root -g root "$binary" "$target"
systemctl start bitcoinwalk-relay
curl --fail --silent --show-error --retry 10 --retry-connrefused --retry-delay 1 --max-time 3 http://127.0.0.1:3334/healthz >/dev/null
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.7.1'
systemctl is-active --quiet bitcoinwalk-relay

trap - EXIT
echo "Single-walk delegation relay policy installed. Protected backup: $backup"
echo "Old city-wide invitations no longer grant editing access. No new invitation was signed. Existing records, direct admin assignments, relay key, Caddy, chat and legacy services retained."
