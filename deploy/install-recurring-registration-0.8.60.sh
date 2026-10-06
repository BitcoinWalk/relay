#!/usr/bin/env bash
set -euo pipefail
test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"
sha256sum -c RECURRING-REGISTRATION-0.8.60-SHA256SUMS
binary="$root_dir/bitcoinwalk-relay-recurring-registration-0.8.60"
installed=/opt/bitcoinwalk-relay/bitcoinwalk-relay
dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/90-bw08-version.conf
service_user=$(systemctl show bitcoinwalk-relay.service --property=User --value)
test -x "$binary";test -f "$installed";test -n "$service_user";test "$service_user" != root
backup=$(mktemp -d /var/backups/bitcoinwalk-recurring-registration.XXXXXX);chmod 0700 "$backup"
stopped=false;cleanup(){ if "$stopped";then systemctl start bitcoinwalk-relay.service||true;fi;};trap cleanup EXIT
cp -p "$installed" "$backup/bitcoinwalk-relay.before";test ! -f "$dropin"||cp -p "$dropin" "$backup/version.before"
systemctl stop bitcoinwalk-relay.service;stopped=true
for name in events.db replication-journal.db;do test ! -f "/var/lib/bitcoinwalk-relay/$name"||cp -p "/var/lib/bitcoinwalk-relay/$name" "$backup/$name";done
(cd "$backup"&&find . -maxdepth 1 -type f ! -name SHA256SUMS -print0|sort -z|xargs -0 sha256sum>SHA256SUMS)
echo "Consistent pre-upgrade relay backup created: $backup"
install -o root -g root -m 0755 "$binary" "$installed"
printf '[Service]\nEnvironment=RELAY_VERSION=bitcoinwalk-organizers-0.8.60\n' >"$dropin";chmod 0644 "$dropin"
systemctl daemon-reload;systemctl reset-failed bitcoinwalk-relay.service||true;systemctl start bitcoinwalk-relay.service;stopped=false
ready=false;for attempt in $(seq 1 30);do if systemctl is-active --quiet bitcoinwalk-relay.service&&curl --fail --silent --max-time 3 http://127.0.0.1:3334/healthz>/dev/null;then ready=true;break;fi;sleep 1;done
"$ready"||{ echo "Relay health check failed. Backup: $backup" >&2;exit 1;}
curl --fail --silent --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/|grep -q 'bitcoinwalk-organizers-0.8.60'
echo "Recurring registration policy 0.8.60 accepted. Backup: $backup"
echo "New city approvals may atomically release one or eight organizer-signed initial walks; legacy single-walk submissions remain supported."
