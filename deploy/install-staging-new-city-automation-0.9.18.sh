#!/usr/bin/env bash
set -euo pipefail
test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")" && pwd); cd "$root_dir"
artifact="$root_dir/bitcoinwalk-relay-new-city-automation-0.9.18"
version_template="$root_dir/98-new-city-automation-0.9.18.conf"
service=bitcoinwalk-relay.service
binary=/opt/bitcoinwalk-relay/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay/events.db
old_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/98-editor-requests-0.9.17.conf
version_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/98-new-city-automation-0.9.18.conf
expected_current_hash=9c0f0683f5592a056e053bbe150c4d2b6f6b31b54cef3d0b8119a7db1c8b3198
expected_current_version=bitcoinwalk-organizers-0.9.17
expected_version=bitcoinwalk-organizers-0.9.18
wait_health(){ local ready=false; for _ in $(seq 1 30);do if curl -fsS --max-time 3 http://127.0.0.1:3334/healthz >/dev/null;then ready=true;break;fi;sleep 1;done;"$ready"; }
logical_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }
sha256sum -c SHA256SUMS
for file in "$artifact" "$version_template" "$binary" "$database" "$old_dropin";do test -f "$file"&&test ! -L "$file";done
test -x "$artifact";test -x "$binary";test ! -e "$version_dropin"
test "$(sha256sum "$binary"|cut -d' ' -f1)" = "$expected_current_hash"
for unit in "$service" bitcoinwalk-relay-production.service bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/|grep -q "$expected_current_version"
if grep -Rqs 'RELAY_NEW_CITY_AUTOMATION_PUBKEY' /etc/systemd/system/bitcoinwalk-relay.service.d;then echo 'Refusing update: new-city automation is unexpectedly enabled.' >&2;exit 1;fi
backup=$(mktemp -d /var/backups/bitcoinwalk-new-city-automation-staging.XXXXXX);chmod 0700 "$backup";cp -p "$binary" "$backup/relay.before";cp -p "$old_dropin" "$backup/version.before.conf"
activated=false;completed=false
recover(){ code=$?;trap - EXIT HUP INT TERM;if "$activated";then systemctl stop "$service" >/dev/null 2>&1||true;install -o root -g root -m 0755 "$backup/relay.before" "$binary";install -o root -g root -m 0644 "$backup/version.before.conf" "$old_dropin";rm -f "$version_dropin";systemctl daemon-reload >/dev/null 2>&1||true;fi;systemctl reset-failed "$service" >/dev/null 2>&1||true;systemctl start "$service" >/dev/null 2>&1||true;if ! "$completed";then echo "New-city relay update failed; staging relay restored. Backup: $backup" >&2;fi;exit "$code"; }
trap recover EXIT HUP INT TERM
database_uid=$(stat -Lc %u "$database");database_gid=$(stat -Lc %g "$database")
systemctl stop "$service";cp -p "$database" "$backup/events.before.db";logical_digest "$backup/events.before.db" >"$backup/events.before.digest"
install -o root -g root -m 0755 "$artifact" "$binary";rm -f "$old_dropin";install -o root -g root -m 0644 "$version_template" "$version_dropin";activated=true
systemctl daemon-reload;systemctl reset-failed "$service"||true;systemctl start "$service";wait_health
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ >"$backup/nip11.after.json";grep -q "$expected_version" "$backup/nip11.after.json"
systemctl restart "$service";wait_health;curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ >"$backup/nip11.after-restart.json";grep -q "$expected_version" "$backup/nip11.after-restart.json"
systemctl stop "$service";cp -p "$database" "$backup/events.after.db";logical_digest "$backup/events.after.db" >"$backup/events.after.digest";cmp "$backup/events.before.digest" "$backup/events.after.digest"
test "$(stat -Lc %u "$database")" = "$database_uid";test "$(stat -Lc %g "$database")" = "$database_gid";systemctl start "$service";wait_health
for unit in "$service" bitcoinwalk-relay-production.service bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
(cd "$backup"&&find . -type f ! -name SHA256SUMS -print0|sort -z|xargs -0 sha256sum >SHA256SUMS)
completed=true;trap - EXIT HUP INT TERM
echo "Bound new-city automation support installed on staging relay 0.9.18. Backup: $backup"
echo 'Automation remains disabled; production and existing signed data were unchanged.'
