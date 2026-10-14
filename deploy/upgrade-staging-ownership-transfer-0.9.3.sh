#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
manifest=OWNERSHIP-TRANSFER-0.9.3-SHA256SUMS
artifact=bitcoinwalk-relay-ownership-0.9.3
target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay/events.db
unit=bitcoinwalk-relay.service
dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/95-ownership-transfer-0.9.3.conf
sha256sum -c "$manifest"
test -x "$artifact";test -x "$target";test -f "$database";systemctl is-active --quiet "$unit"
backup=$(mktemp -d /var/backups/bitcoinwalk-ownership-transfer-staging.XXXXXX);chmod 0700 "$backup"
cp -p "$target" "$backup/bitcoinwalk-relay";cp -p "$database" "$backup/events.db"
if test -e "$dropin";then cp -p "$dropin" "$backup/dropin.before";fi
changed=0
rollback(){ code=$?;trap - EXIT;if test "$code" -ne 0&&test "$changed" -eq 1;then systemctl stop "$unit" 2>/dev/null||true;install -o root -g root -m 0755 "$backup/bitcoinwalk-relay" "$target";if test -e "$backup/dropin.before";then install -o root -g root -m 0644 "$backup/dropin.before" "$dropin";else rm -f "$dropin";fi;systemctl daemon-reload;systemctl start "$unit"||true;echo "Ownership-transfer relay upgrade failed; accepted staging relay restored. Backup: $backup" >&2;fi;exit "$code";};trap rollback EXIT
systemctl stop "$unit";changed=1
install -o root -g root -m 0755 "$artifact" "$target"
install -d -o root -g root -m 0755 "$(dirname "$dropin")"
printf '%s\n' '[Service]' 'Environment=RELAY_VERSION=bitcoinwalk-organizers-0.9.3' >"$backup/dropin.candidate"
install -o root -g root -m 0644 "$backup/dropin.candidate" "$dropin"
systemctl daemon-reload;systemctl start "$unit"
attempt=0;until curl -fsS --max-time 3 http://127.0.0.1:3334/healthz >/dev/null;do attempt=$((attempt+1));test "$attempt" -lt 20||exit 1;sleep 1;done
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.9.3'
systemctl is-active --quiet "$unit"
cmp "$database" "$backup/events.db" >/dev/null || { echo 'Staging database changed during binary-only activation.' >&2;exit 1; }
trap - EXIT
echo "Versioned city ownership-transfer admission accepted on staging relay 0.9.3. Backup: $backup"
echo 'No production relay, directory event, city permission or ownership transfer was created.'
