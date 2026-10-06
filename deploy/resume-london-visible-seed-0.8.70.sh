#!/bin/sh
# Deliver only the eight currently public London occurrences from the isolated
# recovered journal. Historical, hidden and control rows remain excluded.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo on .138.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-london-relay-seed.BACKUP" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=LONDON-VISIBLE-SEED-0.8.70-SHA256SUMS
artifact=./bitcoinwalk-relay-london-visible-seed-0.8.70
audit=./replica-audit-london-visible-seed-0.8.70
original=$1
source_service=bitcoinwalk-relay.service
live_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
live_registry=/etc/bitcoinwalk-replication/registry.json
delivery_key=/etc/bitcoinwalk-replication/replica-delivery-key
city=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
destination=wss://london.bitcoinwalk.org/

case "$original" in /var/backups/bitcoinwalk-london-relay-seed.*) ;; *) echo 'Unexpected seed backup path.' >&2; exit 1;; esac
test -d "$original" && test ! -L "$original"
test "$(stat -c '%U:%G %a' "$original")" = 'root:root 700'
for file in source-baseline.json london-journal.db live-journal.before.db live-registry.before.json; do test -f "$original/$file" && test ! -L "$original/$file"; done
sha256sum -c "$manifest"
for unit in "$source_service" bitcoinwalk-guide.service caddy.service; do systemctl is-active --quiet "$unit"; done
curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz >/dev/null

evidence=$(mktemp -d /var/backups/bitcoinwalk-london-visible-seed.XXXXXX)
chmod 0700 "$evidence"
completed=0
recover() {
  code=$?; trap - EXIT HUP INT TERM
  systemctl start "$source_service" >/dev/null 2>&1 || true
  if test "$completed" -ne 1; then
    echo "London visible-state seed did not complete. Any exact acknowledgements are idempotent and safe to resume. Evidence: $evidence" >&2
  fi
  exit "$code"
}
trap recover EXIT HUP INT TERM

"$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3334 -city "$city" >"$evidence/source-current.json"
python3 - "$original/source-baseline.json" "$evidence/source-current.json" "$city" <<'PY'
import json
import sys

old_path, current_path, city = sys.argv[1:]
with open(old_path, encoding="utf-8") as stream:
    old = json.load(stream)
with open(current_path, encoding="utf-8") as stream:
    current = json.load(stream)
expected = old["source"]["occurrenceIds"]
if old.get("cityId") != city or current.get("cityId") != city:
    raise SystemExit("unexpected city in London source snapshot")
if current["source"]["occurrenceIds"] != expected or current["replica"]["occurrenceIds"] != expected or len(expected) != 8:
    raise SystemExit("London source changed after the first seed attempt")
if not current.get("exactEventIds"):
    raise SystemExit("London source snapshot is not exact")
PY

journal_before=$(sha256sum "$original/london-journal.db" | cut -d ' ' -f 1)
env -i PATH=/usr/bin:/bin \
  RELAY_REPLICA_JOURNAL="$original/london-journal.db" \
  RELAY_REPLICA_DELIVERY_KEY_FILE="$delivery_key" \
  RELAY_REPLICA_VISIBLE_SEED_CITY="$city" \
  RELAY_REPLICA_VISIBLE_SEED_DESTINATION="$destination" \
  RELAY_REPLICA_VISIBLE_SEED_PUBLIC_SNAPSHOT="$original/source-baseline.json" \
  RELAY_REPLICA_VISIBLE_SEED_CONFIRM=london-visible-seed-v1 \
  "$artifact" >"$evidence/seed-result.json"
grep -q '"delivered":8' "$evidence/seed-result.json"
grep -q '"journalMode":"read-only"' "$evidence/seed-result.json"
test "$journal_before" = "$(sha256sum "$original/london-journal.db" | cut -d ' ' -f 1)"

attempt=0
until "$audit" -source ws://127.0.0.1:3334 -replica "$destination" -city "$city" >"$evidence/london.accepted.json" 2>/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 12 || exit 1
  echo 'London visible-state audit has not converged; retrying in 5 seconds.' >&2
  sleep 5
done
python3 - "$evidence/london.accepted.json" "$city" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    result = json.load(stream)
expected = result["source"]["occurrenceIds"]
if result.get("cityId") != sys.argv[2] or len(expected) != 8 or result["replica"]["occurrenceIds"] != expected or not result.get("exactEventIds"):
    raise SystemExit("London receiver is not exact 8/8")
for side in (result["source"], result["replica"]):
    if not side.get("allSignaturesValid") or side.get("privateWrapperCount") != 0:
        raise SystemExit("London receiver failed signature or wrapper validation")
PY

systemctl stop "$source_service"
cp -p "$live_journal" "$evidence/live-journal.after.db"
cp -p "$live_registry" "$evidence/live-registry.after.json"
systemctl start "$source_service"
attempt=0
until curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 30 || exit 1; sleep 1
done
cmp "$original/live-registry.before.json" "$evidence/live-registry.after.json"
before_digest=$(RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$original/live-journal.before.db" "$artifact")
after_digest=$(RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$evidence/live-journal.after.db" "$artifact")
test "$before_digest" = "$after_digest"
for unit in "$source_service" bitcoinwalk-guide.service caddy.service; do systemctl is-active --quiet "$unit"; done
curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz >"$evidence/app-health.after.json"
(
  cd "$evidence"
  sha256sum source-current.json seed-result.json london.accepted.json live-journal.after.db live-registry.after.json app-health.after.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Exact London visible-state seed accepted on 0.8.70. Evidence: $evidence"
echo 'The dedicated receiver is exact 8/8; hidden and historical London rows were excluded.'
echo 'The live registry and stable journal state remained unchanged; no signed directory event was changed.'
