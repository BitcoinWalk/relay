#!/bin/sh
# Seed the public London receiver from a consistent source snapshot through an
# isolated London-only registry and journal. Live registry/journal stay intact.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo on .138.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=LONDON-RELAY-SEED-0.8.69-SHA256SUMS
artifact=./bitcoinwalk-relay-london-seed-0.8.69
audit=./replica-audit-london-seed-0.8.69
source_service=bitcoinwalk-relay.service
source_db=/var/lib/bitcoinwalk-relay/events.db
live_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
live_registry=/etc/bitcoinwalk-replication/registry.json
delivery_key=/etc/bitcoinwalk-replication/replica-delivery-key
city=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
destination=wss://london.bitcoinwalk.org/
port=3359

sha256sum -c "$manifest"
for file in "$source_db" "$live_journal" "$live_registry" "$delivery_key"; do test -f "$file"; done
for unit in "$source_service" bitcoinwalk-guide.service caddy.service; do systemctl is-active --quiet "$unit"; done
curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz >/dev/null
! ss -ltn "sport = :$port" | grep -q LISTEN

backup=$(mktemp -d /var/backups/bitcoinwalk-london-relay-seed.XXXXXX)
chmod 0700 "$backup"
source_copy="$backup/source.db"
journal="$backup/london-journal.db"
registry="$backup/london-registry.json"
pid=
complete=0

stop_isolated() {
  if test -n "$pid"; then kill "$pid" >/dev/null 2>&1 || true; wait "$pid" >/dev/null 2>&1 || true; pid=; fi
}
recover() {
  code=$?; trap - EXIT HUP INT TERM; stop_isolated
  systemctl start "$source_service" >/dev/null 2>&1 || true
  if test "$complete" -ne 1; then echo "London relay seed did not complete; live registry and journal were not changed. Backup: $backup" >&2; fi
  exit "$code"
}
trap recover EXIT HUP INT TERM

"$audit" -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3334 -city "$city" >"$backup/source-baseline.json"
"$audit" -source "$destination" -replica "$destination" -city "$city" -allow-empty >"$backup/london.before.json"
python3 - "$backup/source-baseline.json" "$backup/london.before.json" "$city" <<'PY'
import json
import sys

source_path, receiver_path, city = sys.argv[1:]
with open(source_path, encoding="utf-8") as stream:
    source = json.load(stream)
with open(receiver_path, encoding="utf-8") as stream:
    receiver = json.load(stream)
if source.get("cityId") != city or receiver.get("cityId") != city:
    raise SystemExit("unexpected city in London seed baseline")
if len(source["source"]["occurrenceIds"]) != 8 or not source.get("exactEventIds"):
    raise SystemExit("London source is not the accepted exact 8/8 baseline")
if receiver["source"]["occurrenceIds"] or receiver["replica"]["occurrenceIds"]:
    raise SystemExit("London receiver must be empty before its first seed")
PY

systemctl stop "$source_service"
cp -p "$source_db" "$source_copy"
cp -p "$live_journal" "$backup/live-journal.before.db"
cp -p "$live_registry" "$backup/live-registry.before.json"
systemctl start "$source_service"
attempt=0
until curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 30 || exit 1; sleep 1
done

python3 - "$registry" "$city" "$destination" <<'PY'
import json
import os
import sys
path, city, destination = sys.argv[1:]
data = {"version": 1, "cities": [{"cityId": city, "destination": destination}]}
fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as stream:
    json.dump(data, stream, indent=2)
    stream.write("\n")
PY

env -i PATH=/usr/bin:/bin \
  RELAY_LISTEN="127.0.0.1:$port" \
  RELAY_DB="$source_copy" \
  RELAY_ORGANIZER_MODE=true \
  RELAY_REPLICA_JOURNAL="$journal" \
  RELAY_REPLICA_REGISTRY="$registry" \
  RELAY_REPLICA_DELIVERY_KEY_FILE="$delivery_key" \
  RELAY_VERSION=bitcoinwalk-london-seed-0.8.69 \
  RELAY_NAME='BitcoinWalk isolated London seed' \
  "$artifact" >"$backup/seed.log" 2>&1 &
pid=$!
attempt=0
until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 30 || exit 1; sleep 1
done

attempt=0
until "$audit" -source ws://127.0.0.1:3334 -replica "$destination" -city "$city" >"$backup/london.after.json" 2>/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 12 || exit 1
  echo 'London public audit has not converged; retrying in 5 seconds.' >&2
  sleep 5
done
python3 - "$backup/source-baseline.json" "$backup/london.after.json" "$city" <<'PY'
import json
import sys

source_path, receiver_path, city = sys.argv[1:]
with open(source_path, encoding="utf-8") as stream:
    source = json.load(stream)
with open(receiver_path, encoding="utf-8") as stream:
    receiver = json.load(stream)
expected = source["source"]["occurrenceIds"]
if receiver.get("cityId") != city or receiver["source"]["occurrenceIds"] != expected:
    raise SystemExit("London source changed during seed")
if receiver["replica"]["occurrenceIds"] != expected or len(expected) != 8:
    raise SystemExit("London receiver did not reach exact 8/8 state")
if not receiver.get("exactEventIds"):
    raise SystemExit("London receiver event IDs are not exact")
for side in (receiver["source"], receiver["replica"]):
    if not side.get("allSignaturesValid") or side.get("privateWrapperCount") != 0:
        raise SystemExit("London public audit failed signature or wrapper validation")
PY
stop_isolated
grep -q 'recovered 8 missing record(s)' "$backup/seed.log"
grep -q 'replica delivery acknowledged 8 queued item(s)' "$backup/seed.log"

env -i PATH=/usr/bin:/bin \
  RELAY_DB="$source_copy" \
  RELAY_ORGANIZER_MODE=true \
  RELAY_REPLICA_JOURNAL="$journal" \
  RELAY_REPLICA_REGISTRY="$registry" \
  RELAY_REPLICA_RECONCILIATION_AUDIT=true \
  "$artifact" >"$backup/reconciliation.json"

systemctl stop "$source_service"
cp -p "$live_journal" "$backup/live-journal.after.db"
cp -p "$live_registry" "$backup/live-registry.after.json"
systemctl start "$source_service"
attempt=0
until curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 30 || exit 1; sleep 1
done
cmp "$backup/live-registry.before.json" "$backup/live-registry.after.json"
before_digest=$(RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/live-journal.before.db" "$artifact")
after_digest=$(RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/live-journal.after.db" "$artifact")
test "$before_digest" = "$after_digest"
for unit in "$source_service" bitcoinwalk-guide.service caddy.service; do systemctl is-active --quiet "$unit"; done
curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz >"$backup/app-health.after.json"
(
  cd "$backup"
  sha256sum source.db london-journal.db london-registry.json live-journal.before.db live-journal.after.db live-registry.before.json live-registry.after.json source-baseline.json london.before.json london.after.json seed.log reconciliation.json app-health.after.json >SHA256SUMS
)

complete=1
trap - EXIT HUP INT TERM
echo "Exact London receiver seed accepted on 0.8.69. Evidence: $backup"
echo 'Eight current London occurrences are exact and signature-valid on the dedicated receiver.'
echo 'The live source registry and stable journal state remained unchanged; the signed directory was not changed.'
