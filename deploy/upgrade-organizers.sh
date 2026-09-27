#!/bin/sh
set -eu
if [ "$(id -u)" -ne 0 ]; then echo 'Run with sudo.' >&2; exit 1; fi
cd "$(dirname "$0")/.."
sha256sum -c ORGANIZERS-SHA256SUMS
test -x /opt/bitcoinwalk-relay/bitcoinwalk-relay
test -f /var/lib/bitcoinwalk-relay/events.db
if [ -e /etc/systemd/system/bitcoinwalk-relay.service.d/20-organizers.conf ]; then
 echo 'Organizer override already exists; stop for review.' >&2; exit 1
fi
current_hash=$(sha256sum /opt/bitcoinwalk-relay/bitcoinwalk-relay | cut -d ' ' -f 1)
if [ "$current_hash" != 95b525ed655f2e46649eb867003dde4757185ddc3a1a41486984905fad2d94db ]; then
 echo 'Running binary differs from the verified foundation build; stop for review.' >&2; exit 1
fi
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-relay.XXXXXX)
cp -p /opt/bitcoinwalk-relay/bitcoinwalk-relay "$backup_dir/bitcoinwalk-relay"
systemctl stop bitcoinwalk-relay
changed=0
rollback() {
 code=$?
 if [ "$code" -ne 0 ]; then
  if [ "$changed" -eq 1 ]; then
   systemctl stop bitcoinwalk-relay || true
   install -m 0755 -o root -g root "$backup_dir/bitcoinwalk-relay" /opt/bitcoinwalk-relay/bitcoinwalk-relay
   # Only remove the new drop-in created by this installer. Its source is in this bundle.
   rm -f /etc/systemd/system/bitcoinwalk-relay.service.d/20-organizers.conf
   systemctl daemon-reload
  fi
  systemctl start bitcoinwalk-relay || true
  echo "Upgrade failed. Previous binary restored if changed. Data preserved; backup: $backup_dir" >&2
 fi
}
trap rollback EXIT
# The service is stopped, so this is a consistent database copy.
cp -p /var/lib/bitcoinwalk-relay/events.db "$backup_dir/events.db"
changed=1
install -m 0755 -o root -g root bitcoinwalk-relay-organizers /opt/bitcoinwalk-relay/bitcoinwalk-relay
install -d -m 0755 /etc/systemd/system/bitcoinwalk-relay.service.d
install -m 0644 -o root -g root deploy/20-organizers.conf /etc/systemd/system/bitcoinwalk-relay.service.d/20-organizers.conf
systemctl daemon-reload
systemctl start bitcoinwalk-relay
curl --fail --silent --retry 10 --retry-connrefused --retry-delay 1 --max-time 3 http://127.0.0.1:3334/healthz
curl --fail --silent --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.2.0'
systemctl --no-pager status bitcoinwalk-relay
echo "Organizer mode enabled on staging. Offline backup: $backup_dir"
echo 'DNS, Caddy, production relays and the web-app configuration were not changed.'
