#!/usr/bin/env bash
set -euo pipefail
test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")" && pwd); cd "$root_dir"
artifact="$root_dir/bitcoinwalk-relay-new-city-calendar-release-0.9.19"
version_template="$root_dir/98-new-city-calendar-release-0.9.19.conf"
service=bitcoinwalk-relay.service
binary=/opt/bitcoinwalk-relay/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay/events.db
old_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/98-new-city-automation-0.9.18.conf
version_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/98-new-city-calendar-release-0.9.19.conf
authority_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/99-lublin-new-city-authority.conf
expected_current_hash=3319cf08ffc229facd2eaea1ee84f21c1e958016a8d077093b74a0269eba67b1
expected_current_version=bitcoinwalk-organizers-0.9.18
expected_version=bitcoinwalk-organizers-0.9.19
wait_health(){ local ready=false; for _ in $(seq 1 30);do if curl -fsS --max-time 3 http://127.0.0.1:3334/healthz >/dev/null;then ready=true;break;fi;sleep 1;done;"$ready"; }
logical_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }
sha256sum -c SHA256SUMS
for file in "$artifact" "$version_template" "$binary" "$database" "$old_dropin" "$authority_dropin";do test -f "$file"&&test ! -L "$file";done
test -x "$artifact";test -x "$binary";test ! -e "$version_dropin"
test ! -e /home/bitcoinwalk/releases/bw78-lublin-pilot-20261101-0e1554e/signer-key
test "$(sha256sum "$binary"|cut -d' ' -f1)" = "$expected_current_hash" || { echo 'Unexpected installed staging relay binary.' >&2;exit 1; }
for unit in "$service" bitcoinwalk-relay-production.service bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/|grep -q "$expected_current_version"
grep -q 'RELAY_NEW_CITY_AUTOMATION_PUBKEY=f2d3c5dab98dfcd6c7da495ccb4a0713dd7014b507e4f18493bf4f9a70eb4408' "$authority_dropin"
backup=$(mktemp -d /var/backups/bitcoinwalk-new-city-calendar-release-staging.XXXXXX);chmod 0700 "$backup";cp -p "$binary" "$backup/relay.before";cp -p "$old_dropin" "$backup/version.before.conf"
activated=false;completed=false
recover(){ code=$?;trap - EXIT HUP INT TERM;if "$activated";then systemctl stop "$service" >/dev/null 2>&1||true;install -o root -g root -m 0755 "$backup/relay.before" "$binary";install -o root -g root -m 0644 "$backup/version.before.conf" "$old_dropin";rm -f "$version_dropin";systemctl daemon-reload >/dev/null 2>&1||true;fi;systemctl reset-failed "$service" >/dev/null 2>&1||true;systemctl start "$service" >/dev/null 2>&1||true;if ! "$completed";then echo "Calendar release update failed; staging relay restored. Backup: $backup" >&2;fi;exit "$code"; }
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
echo "Automated Lublin approvals now release their exact initial walks on staging relay 0.9.19. Backup: $backup"
echo 'The private signer remains absent; production and signed data were unchanged.'
