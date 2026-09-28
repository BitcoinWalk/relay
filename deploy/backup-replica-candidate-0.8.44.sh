#!/bin/sh
# Create a consistent, recoverable backup of the isolated candidate receiver on
# the secondary VPS before the first shadow backfill writes to it.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-SHADOW-BACKFILL-0.8.44-SHA256SUMS
audit=./replica-audit-shadow-0.8.44
container=bitcoinwalk-replica-candidate
state=/var/lib/bitcoinwalk-replica-candidate/events.db
candidate=wss://replica.bitcoinwalk.org/
city=be8514a4-9df0-4159-a517-71f65761cbbe

sha256sum -c "$manifest"
for command in docker curl sha256sum mktemp; do command -v "$command" >/dev/null; done
test -f "$state"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-candidate-pre-backfill.XXXXXX)
chmod 0700 "$backup"
completed=0
recover(){
 code=$?;trap - EXIT HUP INT TERM
 docker start "$container" >/dev/null 2>&1||true
 if test "$completed" -ne 1;then echo "Candidate backup did not complete; the container was restarted. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

docker inspect "$container" >"$backup/container.before.json"
docker logs "$container" >"$backup/container.before.log" 2>&1
docker stop -t 20 "$container" >/dev/null
cp -p "$state" "$backup/events.db"
sync "$backup/events.db"
docker start "$container" >/dev/null

attempt=0
until curl --fail --silent --max-time 2 https://replica.bitcoinwalk.org/ >/dev/null;do
 attempt=$((attempt+1));test "$attempt" -lt 30||exit 1;sleep 1
done
"$audit" -source "$candidate" -replica "$candidate" -city "$city" -allow-empty >"$backup/empty-readback.json"
docker inspect "$container" >"$backup/container.after.json"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test -z "$(docker port "$container")"
(
 cd "$backup"
 sha256sum events.db container.before.json container.before.log empty-readback.json container.after.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Consistent pre-backfill candidate backup created: $backup"
echo 'Candidate remained empty, public, private-network-only and restart-stable.'
