#!/usr/bin/env bash
set -Eeuo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd); cd "$root_dir"

checksums=PRODUCTION-SPONSOR-CATALOGUE-0.9.12-SHA256SUMS
artifact="$root_dir/bitcoinwalk-relay-production-sponsor-catalogue-0.9.12"
version_template="$root_dir/deploy/zzz-production-sponsor-catalogue-version-0.9.12.conf"
service=bitcoinwalk-relay-production.service
binary=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay-production/events.db
journal=/var/lib/bitcoinwalk-relay-production/replication-journal.db
registry=/etc/bitcoinwalk-replication-production/registry.json
ledger=/etc/bitcoinwalk-replication-production/entitlements.json
version_dropin=/etc/systemd/system/bitcoinwalk-relay-production.service.d/zzz-sponsor-catalogue-version.conf
expected_current=bitcoinwalk-production-initial-history-v2
expected_version=bitcoinwalk-production-sponsor-catalogue-0.9.12

wait_health(){
 local ready=false
 for _ in $(seq 1 40); do
  if curl --fail --silent --max-time 3 http://127.0.0.1:3340/healthz >/dev/null; then ready=true; break; fi
  sleep 1
 done
 "$ready"
}
logical_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }
journal_digest(){ RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$1" "$artifact"; }

sha256sum -c "$checksums"
for command in curl sha256sum mktemp install systemctl stat cmp; do command -v "$command" >/dev/null; done
for file in "$artifact" "$version_template" "$binary" "$database" "$journal" "$registry" "$ledger"; do test -f "$file" && test ! -L "$file"; done
test -x "$artifact"; test -x "$binary"; test ! -e "$version_dropin"
for unit in "$service" bitcoinwalk-relay.service bitcoinwalk-guide.service; do systemctl is-active --quiet "$unit"; done
curl --fail --silent --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ | grep -q "$expected_current"
curl --fail --silent --max-time 5 http://127.0.0.1:3345/api/healthz | grep -q '"release":"app-production-0.3.526"'

backup=$(mktemp -d /var/backups/bitcoinwalk-production-sponsor-catalogue.XXXXXX); chmod 0700 "$backup"
cp -p "$binary" "$backup/relay.before"
cp -p "$registry" "$backup/registry.before.json"
cp -p "$ledger" "$backup/entitlements.before.json"
curl --fail --silent --max-time 5 http://127.0.0.1:3345/api/healthz >"$backup/app-health.before.json"
activated=false; completed=false; failure_line=unknown; failure_command=unknown
trap 'failure_line=$LINENO; failure_command=$BASH_COMMAND' ERR
recover(){
 code=$?; trap - EXIT HUP INT TERM
 if "$activated"; then
  systemctl status "$service" --no-pager -l >"$backup/candidate-status.txt" 2>&1 || true
  journalctl -u "$service" --since "5 minutes ago" --no-pager -n 120 >"$backup/candidate-journal.txt" 2>&1 || true
  curl --silent --show-error --max-time 3 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/candidate-nip11.json" 2>"$backup/candidate-curl-error.txt" || true
  systemctl stop "$service" >/dev/null 2>&1 || true
  install -o root -g root -m 0755 "$backup/relay.before" "$binary"
  rm -f "$version_dropin"
  systemctl daemon-reload >/dev/null 2>&1 || true
 fi
 systemctl reset-failed "$service" >/dev/null 2>&1 || true
 systemctl start "$service" >/dev/null 2>&1 || true
 if ! "$completed"; then
  echo "Production sponsor-catalogue upgrade failed at line $failure_line: $failure_command" >&2
  echo "Accepted production relay restored. Backup: $backup" >&2
 fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

database_uid=$(stat -Lc %u "$database"); database_gid=$(stat -Lc %g "$database")
journal_uid=$(stat -Lc %u "$journal"); journal_gid=$(stat -Lc %g "$journal")
systemctl stop "$service"
cp -p "$database" "$backup/events.before.db"
cp -p "$journal" "$backup/journal.before.db"
logical_digest "$backup/events.before.db" >"$backup/events.before.digest"
journal_digest "$backup/journal.before.db" >"$backup/journal.before.digest"
install -o root -g root -m 0755 "$artifact" "$binary"
install -o root -g root -m 0644 "$version_template" "$version_dropin"
activated=true
systemctl daemon-reload; systemctl reset-failed "$service" || true; systemctl start "$service"; wait_health
curl --fail --silent --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/nip11.after.json"
grep -q "$expected_version" "$backup/nip11.after.json"

systemctl restart "$service"; wait_health
curl --fail --silent --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/nip11.after-restart.json"
grep -q "$expected_version" "$backup/nip11.after-restart.json"
systemctl stop "$service"
cp -p "$database" "$backup/events.after.db"
cp -p "$journal" "$backup/journal.after.db"
logical_digest "$backup/events.after.db" >"$backup/events.after.digest"
journal_digest "$backup/journal.after.db" >"$backup/journal.after.digest"
cmp "$backup/events.before.digest" "$backup/events.after.digest"
cmp "$backup/journal.before.digest" "$backup/journal.after.digest"
test "$(stat -Lc %u "$database")" = "$database_uid"; test "$(stat -Lc %g "$database")" = "$database_gid"
test "$(stat -Lc %u "$journal")" = "$journal_uid"; test "$(stat -Lc %g "$journal")" = "$journal_gid"
systemctl start "$service"; wait_health

cmp "$backup/registry.before.json" "$registry"
cmp "$backup/entitlements.before.json" "$ledger"
curl --fail --silent --max-time 5 http://127.0.0.1:3345/api/healthz >"$backup/app-health.after.json"
cmp "$backup/app-health.before.json" "$backup/app-health.after.json"
for unit in "$service" bitcoinwalk-relay.service bitcoinwalk-guide.service; do systemctl is-active --quiet "$unit"; done

(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
completed=true; trap - ERR EXIT HUP INT TERM
echo "Production sponsor catalogue policy accepted on relay 0.9.12. Backup: $backup"
echo 'Existing signed events, database ownership, replication journal, registry and entitlement ledger remained exact across two restarts.'
