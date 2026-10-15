#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this recovery with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd)
cd "$root_dir"

checksums=PRODUCTION-RECONCILIATION-0.9.6-SHA256SUMS
artifact="$root_dir/bitcoinwalk-relay-production-reconciliation-0.9.6"
audit="$root_dir/replica-audit-production-additive-0.8.85"
builder="$root_dir/bitcoinwalk-relay-production-additive-0.8.85"
dropin="$root_dir/deploy/99-production-reconciliation-version-0.9.6.conf"
service=bitcoinwalk-relay-production.service
installed=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
installed_dropin=/etc/systemd/system/bitcoinwalk-relay-production.service.d/99-production-reconciliation-version.conf
events=/var/lib/bitcoinwalk-relay-production/events.db
journal=/var/lib/bitcoinwalk-relay-production/replication-journal.db
london=ca2f9905-fb4d-4948-a12c-c792b28ec7c8

wait_health() {
  local url=$1
  for _ in $(seq 1 90); do
    if curl --fail --silent --max-time 3 "$url" >/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

verify_audit() {
  python3 - "$1" "$2" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as stream: report=json.load(stream)
if report.get("cityId")!=sys.argv[2] or not report.get("readOnly") or not report.get("exactEventIds"):
 raise SystemExit("city audit is not exact and read-only")
if report["source"]["occurrenceIds"]!=report["replica"]["occurrenceIds"]:
 raise SystemExit("city occurrence IDs differ")
for side in ("source","replica"):
 if not report[side]["allSignaturesValid"] or report[side]["privateWrapperCount"]!=0:
  raise SystemExit("city audit failed signature or privacy acceptance")
PY
}

sha256sum -c "$checksums"
for command in curl install journalctl mktemp python3 sha256sum systemctl; do command -v "$command" >/dev/null; done
for file in "$artifact" "$audit" "$builder" "$dropin" "$installed" "$events" "$journal"; do test -f "$file" && test ! -L "$file"; done
test -x "$artifact"; test -x "$audit"; test -x "$builder"; test -x "$installed"

backup=$(mktemp -d /var/backups/bitcoinwalk-production-reconciliation.XXXXXX)
chmod 0700 "$backup"
cp -p "$installed" "$backup/bitcoinwalk-relay.before"
cp -p "$events" "$backup/events.before.db"
cp -p "$journal" "$backup/replication-journal.before.db"
if test -f "$installed_dropin"; then cp -p "$installed_dropin" "$backup/version.before.conf"; fi
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/replication-journal.before.db" "$builder" >"$backup/journal.before.digest"

install -o root -g root -m 0755 "$artifact" "$installed"
install -o root -g root -m 0644 "$dropin" "$installed_dropin"
systemctl daemon-reload
systemctl reset-failed "$service" || true
systemctl restart "$service"
if ! wait_health http://127.0.0.1:3340/healthz; then
  journalctl -u "$service" --since "5 minutes ago" --no-pager -n 40 >"$backup/failed-start.log" || true
  echo "Production relay recovery did not reach health. Evidence: $backup" >&2
  exit 1
fi

RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$journal" "$builder" >"$backup/journal.after.digest"
cmp "$backup/journal.before.digest" "$backup/journal.after.digest"
"$audit" -source ws://127.0.0.1:3340 -replica wss://london.bitcoinwalk.org/ -city "$london" >"$backup/london.after.json"
verify_audit "$backup/london.after.json" "$london"
systemctl is-active --quiet "$service"
curl -fsS --max-time 10 http://127.0.0.1:3340/healthz >"$backup/health.after.json"
(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)

echo "Production relay reconciliation recovery accepted on 0.9.6. Evidence: $backup"
echo "The exact production database and replication journal were preserved; London remained exact after restart."
echo "The relay can now safely resume the separate Lugano additive promotion."
