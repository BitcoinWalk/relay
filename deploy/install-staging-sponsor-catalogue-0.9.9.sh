#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd); cd "$root_dir"

checksums=SPONSOR-CATALOGUE-0.9.9-SHA256SUMS
artifact="$root_dir/bitcoinwalk-relay-sponsor-catalogue-0.9.9"
version_template="$root_dir/deploy/96-sponsor-catalogue-version-0.9.9.conf"
service=bitcoinwalk-relay.service
binary=/opt/bitcoinwalk-relay/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay/events.db
version_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/96-walk-content-image-version.conf
expected_current=bitcoinwalk-organizers-0.9.8
expected_version=bitcoinwalk-organizers-0.9.9

wait_health(){
 local ready=false
 for _ in $(seq 1 30); do
  if curl --fail --silent --max-time 3 http://127.0.0.1:3334/healthz >/dev/null; then ready=true; break; fi
  sleep 1
 done
 "$ready"
}
logical_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }

sha256sum -c "$checksums"
for command in curl sha256sum mktemp install systemctl stat cmp; do command -v "$command" >/dev/null; done
for file in "$artifact" "$version_template" "$binary" "$database" "$version_dropin"; do test -f "$file" && test ! -L "$file"; done
test -x "$artifact"; test -x "$binary"
for unit in "$service" bitcoinwalk-relay-production.service bitcoinwalk-guide.service; do systemctl is-active --quiet "$unit"; done
curl --fail --silent --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q "$expected_current"

backup=$(mktemp -d /var/backups/bitcoinwalk-sponsor-catalogue-staging.XXXXXX); chmod 0700 "$backup"
cp -p "$binary" "$backup/relay.before"
cp -p "$version_dropin" "$backup/version.before.conf"
activated=false; completed=false
recover(){
 code=$?; trap - EXIT HUP INT TERM
 if "$activated"; then
  systemctl stop "$service" >/dev/null 2>&1 || true
  install -o root -g root -m 0755 "$backup/relay.before" "$binary"
  install -o root -g root -m 0644 "$backup/version.before.conf" "$version_dropin"
  systemctl daemon-reload >/dev/null 2>&1 || true
 fi
 systemctl reset-failed "$service" >/dev/null 2>&1 || true
 systemctl start "$service" >/dev/null 2>&1 || true
 if ! "$completed"; then echo "Sponsor-catalogue relay upgrade failed; accepted staging relay restored. Backup: $backup" >&2; fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

database_uid=$(stat -Lc %u "$database"); database_gid=$(stat -Lc %g "$database")
systemctl stop "$service"
cp -p "$database" "$backup/events.before.db"
logical_digest "$backup/events.before.db" >"$backup/events.before.digest"
install -o root -g root -m 0755 "$artifact" "$binary"
install -o root -g root -m 0644 "$version_template" "$version_dropin"
activated=true
systemctl daemon-reload; systemctl reset-failed "$service" || true; systemctl start "$service"; wait_health
curl --fail --silent --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ >"$backup/nip11.after.json"
grep -q "$expected_version" "$backup/nip11.after.json"

systemctl restart "$service"; wait_health
curl --fail --silent --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ >"$backup/nip11.after-restart.json"
grep -q "$expected_version" "$backup/nip11.after-restart.json"
systemctl stop "$service"
cp -p "$database" "$backup/events.after.db"
logical_digest "$backup/events.after.db" >"$backup/events.after.digest"
cmp "$backup/events.before.digest" "$backup/events.after.digest"
test "$(stat -Lc %u "$database")" = "$database_uid"; test "$(stat -Lc %g "$database")" = "$database_gid"
systemctl start "$service"; wait_health
for unit in "$service" bitcoinwalk-relay-production.service bitcoinwalk-guide.service; do systemctl is-active --quiet "$unit"; done

(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
completed=true; trap - EXIT HUP INT TERM
echo "Reusable sponsor catalogue policy accepted on staging relay 0.9.9. Backup: $backup"
echo 'Existing signed events and database ownership remained exact across two restarts; production was unchanged.'
