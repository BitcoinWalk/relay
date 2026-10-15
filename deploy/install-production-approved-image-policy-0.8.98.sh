#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"

checksums=PRODUCTION-APPROVED-IMAGE-POLICY-0.8.98-SHA256SUMS
artifact="$root_dir/bitcoinwalk-relay-production-approved-image-0.8.98"
audit="$root_dir/replica-audit-production-approved-image-0.8.98"
version_template="$root_dir/deploy/99-production-approved-image-version-0.8.98.conf"
service=bitcoinwalk-relay-production.service
binary=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay-production/events.db
journal=/var/lib/bitcoinwalk-relay-production/replication-journal.db
registry=/etc/bitcoinwalk-replication-production/registry.json
ledger=/etc/bitcoinwalk-replication-production/entitlements.json
version_dropin=/etc/systemd/system/bitcoinwalk-relay-production.service.d/99-approved-image-policy-version.conf
london=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
london_relay=wss://london.bitcoinwalk.org/
expected_version=bitcoinwalk-production-approved-image-0.8.98

wait_health(){
 local url=$1 ready=false
 for attempt in $(seq 1 40);do
  if curl --fail --silent --max-time 3 "$url" >/dev/null;then ready=true;break;fi
  sleep 1
 done
 "$ready"
}
verify_audit(){
 python3 - "$1" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: report=json.load(stream)
if not report.get("readOnly") or not report.get("exactEventIds"):
    raise SystemExit("London audit is not exact and read-only")
if report["source"]["occurrenceIds"] != report["replica"]["occurrenceIds"]:
    raise SystemExit("London occurrence IDs differ")
for side in ("source", "replica"):
    state=report[side]
    if not state["allSignaturesValid"] or state["privateWrapperCount"] != 0:
        raise SystemExit("London audit failed signature or privacy acceptance")
PY
}
logical_digest(){ RELAY_REPLICA_DB_DIGEST="$1" "$artifact"; }
journal_digest(){ RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$1" "$artifact"; }

sha256sum -c "$checksums"
for command in curl python3 sha256sum mktemp install systemctl stat grep cmp;do command -v "$command" >/dev/null;done
for file in "$artifact" "$audit" "$version_template" "$binary" "$database" "$journal" "$registry" "$ledger";do test -f "$file" && test ! -L "$file";done
test -x "$artifact";test -x "$audit";test ! -e "$version_dropin"
for unit in "$service" bitcoinwalk-relay.service bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3340/healthz >/dev/null
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3345/api/healthz | grep -q '"release":"app-production-0.3.349"'
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ | grep -q 'bitcoinwalk-production-london-0.8.78'
systemctl cat "$service" | grep -q 'RELAY_REPLICA_REGISTRY=/etc/bitcoinwalk-replication-production/registry.json'

backup=$(mktemp -d /var/backups/bitcoinwalk-production-approved-image.XXXXXX);chmod 0700 "$backup"
cp -p "$binary" "$backup/production-binary.before"
cp -p "$registry" "$backup/registry.before.json"
cp -p "$ledger" "$backup/entitlements.before.json"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3345/api/healthz >"$backup/app-health.before.json"

activated=false;completed=false
recover(){
 code=$?;trap - EXIT HUP INT TERM
 if "$activated";then
  systemctl stop "$service" >/dev/null 2>&1||true
  install -o root -g root -m 0755 "$backup/production-binary.before" "$binary"
  rm -f "$version_dropin"
  systemctl daemon-reload >/dev/null 2>&1||true
 fi
 systemctl reset-failed "$service" >/dev/null 2>&1||true
 systemctl start "$service" >/dev/null 2>&1||true
 if ! "$completed";then echo "Production approved-image policy did not complete; prior production binary and version were restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

database_uid=$(stat -Lc %u "$database");database_gid=$(stat -Lc %g "$database")
systemctl stop "$service"
cp -p "$database" "$backup/production-events.before.db"
cp -p "$journal" "$backup/production-journal.before.db"
logical_digest "$backup/production-events.before.db" >"$backup/production-events.before.digest"
journal_digest "$backup/production-journal.before.db" >"$backup/production-journal.before.digest"
install -o root -g root -m 0755 "$artifact" "$binary"
install -o root -g root -m 0644 "$version_template" "$version_dropin"
activated=true
systemctl daemon-reload;systemctl reset-failed "$service"||true
systemctl start "$service";wait_health http://127.0.0.1:3340/healthz

curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/nip11.after.json"
grep -q "$expected_version" "$backup/nip11.after.json"
"$audit" -source ws://127.0.0.1:3340 -replica "$london_relay" -city "$london" >"$backup/london.after.json"
verify_audit "$backup/london.after.json"

systemctl restart "$service";wait_health http://127.0.0.1:3340/healthz
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/nip11.after-restart.json"
grep -q "$expected_version" "$backup/nip11.after-restart.json"
"$audit" -source ws://127.0.0.1:3340 -replica "$london_relay" -city "$london" >"$backup/london.after-restart.json"
verify_audit "$backup/london.after-restart.json"
cmp "$backup/london.after.json" "$backup/london.after-restart.json"

systemctl stop "$service"
cp -p "$database" "$backup/production-events.after.db"
cp -p "$journal" "$backup/production-journal.after.db"
logical_digest "$backup/production-events.after.db" >"$backup/production-events.after.digest"
journal_digest "$backup/production-journal.after.db" >"$backup/production-journal.after.digest"
cmp "$backup/production-events.before.digest" "$backup/production-events.after.digest"
cmp "$backup/production-journal.before.digest" "$backup/production-journal.after.digest"
test "$(stat -Lc %u "$database")" = "$database_uid";test "$(stat -Lc %g "$database")" = "$database_gid"
systemctl start "$service";wait_health http://127.0.0.1:3340/healthz

cmp "$backup/registry.before.json" "$registry"
cmp "$backup/entitlements.before.json" "$ledger"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3345/api/healthz >"$backup/app-health.after.json"
cmp "$backup/app-health.before.json" "$backup/app-health.after.json"
for unit in "$service" bitcoinwalk-relay.service bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done

(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
completed=true;trap - EXIT HUP INT TERM
echo "Approved city-image occurrence policy accepted on 0.8.98. Backup: $backup"
echo 'Production event and replication-journal logical digests remained exact across two restarts; London replication remained exact.'
echo 'Occurrence publishing still requires the approved city image; the relay now honors the super-admin-approved image override.'
