#!/bin/sh
# Staging-only organizer-author cancellation policy. Does not sign or cancel any event.
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

binary=bitcoinwalk-relay-cancellation-0.6.1
target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay/events.db
test -f "$binary"
printf '%s  %s\n' '3186a14f8fda210ed173e33269e387d068b7e840d5a66e291db72d4fe81dbe82' "$binary" | sha256sum -c -
systemctl is-active --quiet bitcoinwalk-relay
printf '%s  %s\n' '39368ca9dfc5e00e47644d5fa9c94c6b9f4e8dcd71251c6faea378ecb5546075' "$target" | sha256sum -c -
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.6.0'
test -f "$database"

backup=$(mktemp -d /var/backups/bitcoinwalk-calendar-cancellation.XXXXXX)
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
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.6.1'
systemctl is-active --quiet bitcoinwalk-relay

trap - EXIT
echo "Organizer cancellation relay policy installed. Protected backup: $backup"
echo "No event was cancelled. Existing records, relay key, Caddy, chat and legacy services were not changed."
