#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if ss -lnt | grep -q ':3334 '; then
 echo 'Port 3334 is already occupied; not starting a test instance.' >&2
 exit 1
fi
RELAY_DB="$PWD/smoke-data/events.db" ./bitcoinwalk-relay &
relay_pid=$!
trap 'kill -TERM "$relay_pid" 2>/dev/null || true; wait "$relay_pid" 2>/dev/null || true' EXIT INT TERM
attempt=0
until curl --fail --silent --max-time 2 http://127.0.0.1:3334/healthz; do
 attempt=$((attempt + 1))
 if [ "$attempt" -ge 20 ]; then exit 1; fi
 sleep 0.2
done
curl --fail --silent --max-time 2 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/
ss -lnt
