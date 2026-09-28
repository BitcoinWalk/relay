#!/bin/sh
# Reset the isolated 0.8.48 candidate to its verified empty pre-backfill state
# before the exact-public-snapshot seed. Preserve the current divergent state.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-replica-candidate-pre-backfill.BACKUP" >&2; exit 1; }
cd "$(dirname "$0")/.."

restore=$1
case "$restore" in /var/backups/bitcoinwalk-replica-candidate-pre-backfill.*) ;; *) echo 'Refusing an unexpected restore path.' >&2;exit 1;; esac
test ! -L "$restore";test -d "$restore";test -f "$restore/events.db";test -f "$restore/SHA256SUMS"
(cd "$restore" && sha256sum -c SHA256SUMS)

manifest=REPLICA-CANDIDATE-VISIBLE-RESET-0.8.49-SHA256SUMS
audit=./replica-audit-shadow-0.8.44
state=/var/lib/bitcoinwalk-replica-candidate/events.db
container=bitcoinwalk-replica-candidate
candidate=wss://replica.bitcoinwalk.org/
city=be8514a4-9df0-4159-a517-71f65761cbbe

sha256sum -c "$manifest"
for command in docker curl python3 sha256sum mktemp install grep;do command -v "$command" >/dev/null;done
test -f "$state"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' https://replica.bitcoinwalk.org/ | grep -Eq '"version"[[:space:]]*:[[:space:]]*"bitcoinwalk-replica-candidate-0.8.48"'

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-candidate-visible-reset.XXXXXX)
chmod 0700 "$backup"
docker inspect "$container" >"$backup/container.before.json"
docker logs "$container" >"$backup/container.before.log" 2>&1
"$audit" -source "$candidate" -replica "$candidate" -city "$city" -allow-empty >"$backup/readback.before.json"
completed=0
changed=0
recover(){
 code=$?;trap - EXIT HUP INT TERM
 if test "$changed" -eq 1;then
  docker stop -t 20 "$container" >/dev/null 2>&1||true
  install -o 65532 -g 65532 -m 0600 "$backup/events.divergent.db" "$state"
  docker start "$container" >/dev/null 2>&1||true
 fi
 if test "$completed" -ne 1;then echo "Candidate visible-state reset did not complete; prior state was restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

docker stop -t 20 "$container" >/dev/null
cp -p "$state" "$backup/events.divergent.db"
sync "$backup/events.divergent.db"
changed=1
install -o 65532 -g 65532 -m 0600 "$restore/events.db" "$state"
docker start "$container" >/dev/null

attempt=0
until curl --fail --silent --max-time 2 https://replica.bitcoinwalk.org/ >/dev/null;do
 attempt=$((attempt+1));test "$attempt" -lt 30||exit 1;sleep 1
done
docker inspect "$container" >"$backup/container.after.json"
python3 - "$backup/container.after.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    container = json.load(stream)[0]
if not container["State"]["Running"] or not container["HostConfig"]["ReadonlyRootfs"]:
    raise SystemExit("candidate runtime hardening changed")
if container["HostConfig"]["PortBindings"]:
    raise SystemExit("candidate unexpectedly publishes a host port")
if set(container["HostConfig"].get("Tmpfs", {})) != {"/tmp"}:
    raise SystemExit("candidate validation tmpfs is unavailable")
PY
"$audit" -source "$candidate" -replica "$candidate" -city "$city" -allow-empty >"$backup/readback.after.json"
python3 - "$backup/readback.after.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if report["source"]["occurrenceIds"] or report["replica"]["occurrenceIds"]:
    raise SystemExit("reset candidate is not empty")
if not report["exactEventIds"] or not report["readOnly"]:
    raise SystemExit("reset candidate audit is not exact and read-only")
PY
test -z "$(docker port "$container")"
(
 cd "$backup"
 sha256sum events.divergent.db container.before.json container.before.log readback.before.json container.after.json readback.after.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Empty candidate visible-state reset accepted on 0.8.49. Backup: $backup"
echo "Restored database source: $restore"
echo 'The prior divergent database remains recoverable as events.divergent.db; candidate 0.8.48 is exact 0/0 and ready for the 0.8.49 visible-state backfill.'
