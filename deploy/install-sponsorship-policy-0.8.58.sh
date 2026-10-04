#!/usr/bin/env bash
set -euo pipefail

test "${EUID}" -eq 0 || { echo "Run with sudo." >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd)
binary="$root_dir/bitcoinwalk-relay-sponsorships-0.8.58"
installed=/opt/bitcoinwalk-relay/bitcoinwalk-relay
dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/90-bw08-version.conf
backup=$(mktemp -d /var/backups/bitcoinwalk-sponsorship-policy.XXXXXX)
accepted=false

restore(){
 if "$accepted"; then return; fi
 systemctl stop bitcoinwalk-relay.service 2>/dev/null || true
 test ! -f "$backup/bitcoinwalk-relay" || install -o root -g root -m 0755 "$backup/bitcoinwalk-relay" "$installed"
 if test -f "$backup/version.conf"; then install -o root -g root -m 0644 "$backup/version.conf" "$dropin"; else rm -f "$dropin"; fi
 systemctl daemon-reload
 systemctl reset-failed bitcoinwalk-relay.service || true
 systemctl start bitcoinwalk-relay.service || true
 echo "0.8.58 activation failed; accepted relay restored. Backup: $backup" >&2
}
trap restore EXIT

test -x "$binary"
systemctl stop bitcoinwalk-relay.service
cp -p "$installed" "$backup/bitcoinwalk-relay"
test ! -f "$dropin" || cp -p "$dropin" "$backup/version.conf"
test ! -f /var/lib/bitcoinwalk-relay/events.db || cp -p /var/lib/bitcoinwalk-relay/events.db "$backup/events.db"
test ! -f /var/lib/bitcoinwalk-relay/replication-journal.db || cp -p /var/lib/bitcoinwalk-relay/replication-journal.db "$backup/replication-journal.db"
echo "Consistent pre-activation relay backup created: $backup"

install -o root -g root -m 0755 "$binary" "$installed"
install -d -o root -g root -m 0755 "$(dirname "$dropin")"
printf '[Service]\nEnvironment=RELAY_VERSION=bitcoinwalk-organizers-0.8.58\n' >"$dropin"
chmod 0644 "$dropin"
systemctl daemon-reload
systemctl reset-failed bitcoinwalk-relay.service
systemctl start bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-relay.service
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3334/healthz >/dev/null
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.58'

accepted=true
echo "Dedicated sponsorship policy accepted on relay 0.8.58. Backup: $backup"
echo "Kind 30308 remains feature-flags-only; signed sponsorship assignments use kind 30311."
