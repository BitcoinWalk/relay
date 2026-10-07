#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-MULTICITY-0.8.79-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-multicity-0.8.79
unit_source=./deploy/bitcoinwalk-directory-staging-0.8.79.service
collector=./deploy/capture-city-directory-root-0.8.35.mjs
anchors_source=./deploy/city-directory-anchors-0.8.34.json
bundle_source=./deploy/city-directory-memphis-root-0.8.34.json
target=/opt/bitcoinwalk-directory-staging/bitcoinwalk-relay
unit=/etc/systemd/system/bitcoinwalk-directory-staging.service
database=/var/lib/bitcoinwalk-directory-staging/events.db
anchors=/etc/bitcoinwalk-directory/anchors.json
bundle=/etc/bitcoinwalk-directory/memphis-bundle.json
service=bitcoinwalk-directory-staging.service
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
root_event=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
old_sha=5588febe05886ad6d9a5ac4bb06f591ab53315f5825c771a411cbaba25e180e4

sha256sum -c "$manifest"
test "$(sha256sum "$target" | cut -d ' ' -f 1)" = "$old_sha" || { echo 'Installed primary directory binary is not the accepted predecessor.' >&2; exit 1; }
cmp -s "$anchors" "$anchors_source"; cmp -s "$bundle" "$bundle_source"
test -x "$node"; systemctl is-active --quiet "$service"

backup=$(mktemp -d /var/backups/bitcoinwalk-directory-multicity-primary.XXXXXX)
chmod 0700 "$backup"
cp -p "$target" "$backup/bitcoinwalk-relay.before"
cp -p "$unit" "$backup/service.before"
cp -p "$anchors" "$backup/anchors.json"
cp -p "$bundle" "$backup/memphis-bundle.json"
"$node" "$collector" wss://directory-staging.bitcoinwalk.org/ "$root_event" >"$backup/public.before.json"

changed=0
completed=0
rollback(){
 code=$?; trap - EXIT HUP INT TERM
 if test "$changed" -eq 1; then
  systemctl stop "$service" >/dev/null 2>&1 || true
  install -o root -g root -m 0755 "$backup/bitcoinwalk-relay.before" "$target"
  install -o root -g root -m 0644 "$backup/service.before" "$unit"
  test ! -f "$backup/events.db" || cp -p "$backup/events.db" "$database"
  systemctl daemon-reload
  systemctl start "$service" >/dev/null 2>&1 || true
 fi
 test "$completed" -eq 1 || echo "Primary multi-city upgrade did not complete; accepted service restored. Backup: $backup" >&2
 exit "$code"
}
trap rollback EXIT HUP INT TERM

systemctl stop "$service"
cp -p "$database" "$backup/events.db"
sync "$backup/events.db"
changed=1
install -o root -g root -m 0755 "$artifact" "$target"
install -o root -g root -m 0644 "$unit_source" "$unit"
systemctl daemon-reload
systemctl start "$service"

wait_health(){
 attempt=0
 until curl --fail --silent --max-time 2 http://127.0.0.1:3343/healthz >/dev/null; do attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1; done
}
capture(){ "$node" "$collector" "$1" "$root_event" >"$2"; }
wait_health
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3343/ >"$backup/nip11.json"
grep -q 'bitcoinwalk-directory-transport-0.8.79' "$backup/nip11.json"
capture ws://127.0.0.1:3343/ "$backup/loopback.before-restart.json"
systemctl restart "$service"
wait_health
capture ws://127.0.0.1:3343/ "$backup/loopback.after-restart.json"
capture wss://directory-staging.bitcoinwalk.org/ "$backup/public.after.json"
cmp "$backup/public.before.json" "$backup/public.after.json"
cmp "$backup/loopback.before-restart.json" "$backup/loopback.after-restart.json"
cmp "$backup/public.after.json" "$backup/loopback.after-restart.json"
systemctl is-active --quiet "$service"
systemctl show "$service" -p MainPID -p User -p DynamicUser >"$backup/service.after.txt"
(
 cd "$backup"
 sha256sum bitcoinwalk-relay.before service.before anchors.json memphis-bundle.json events.db public.before.json public.after.json loopback.before-restart.json loopback.after-restart.json nip11.json service.after.txt >SHA256SUMS
)
completed=1
trap - EXIT HUP INT TERM
echo "Primary multi-city-capable directory transport 0.8.79 accepted. Backup: $backup"
echo "Memphis root remained exact across activation and restart: $root_event"
echo 'No anchor, signed event, DNS, Caddy, application, Guide, relay or replica state was changed.'
