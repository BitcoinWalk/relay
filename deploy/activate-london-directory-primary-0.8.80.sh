#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-LONDON-ACTIVATION-0.8.80-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-multicity-0.8.79
collector=./deploy/capture-city-directory-root-0.8.35.mjs
old_anchors=./deploy/city-directory-anchors-0.8.34.json
old_bundle=./deploy/city-directory-memphis-root-0.8.34.json
new_anchors=./deploy/city-directory-anchors-0.8.80.json
new_bundle=./deploy/city-directory-memphis-london-bundle-0.8.80.json
anchors=/etc/bitcoinwalk-directory/anchors.json
bundle=/etc/bitcoinwalk-directory/memphis-bundle.json
database=/var/lib/bitcoinwalk-directory-staging/events.db
service=bitcoinwalk-directory-staging.service
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
memphis=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
london=d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2

sha256sum -c "$manifest"
cmp -s "$anchors" "$old_anchors"; cmp -s "$bundle" "$old_bundle"
test -x "$node"; systemctl is-active --quiet "$service"
for city in be8514a4-9df0-4159-a517-71f65761cbbe ca2f9905-fb4d-4948-a12c-c792b28ec7c8; do
 RELAY_CITY_DIRECTORY_AUDIT_ANCHORS="$PWD/$new_anchors" RELAY_CITY_DIRECTORY_AUDIT_BUNDLE="$PWD/$new_bundle" RELAY_CITY_DIRECTORY_AUDIT_CITY="$city" "$artifact" >/dev/null
done
backup=$(mktemp -d /var/backups/bitcoinwalk-directory-london-primary.XXXXXX); chmod 0700 "$backup"
cp -p "$anchors" "$backup/anchors.before.json"; cp -p "$bundle" "$backup/bundle.before.json"
"$node" "$collector" wss://directory-staging.bitcoinwalk.org/ "$memphis" >"$backup/memphis.public.before.json"
if "$node" "$collector" wss://directory-staging.bitcoinwalk.org/ "$london" >"$backup/london.unexpected.before.json" 2>/dev/null; then echo 'London root is already public; stop for audit.' >&2; exit 1; fi

changed=0; completed=0
rollback(){
 code=$?; trap - EXIT HUP INT TERM
 if test "$changed" -eq 1; then
  systemctl stop "$service" >/dev/null 2>&1 || true
  install -o root -g root -m 0644 "$backup/anchors.before.json" "$anchors"
  install -o root -g root -m 0644 "$backup/bundle.before.json" "$bundle"
  cp -p "$backup/events.before.db" "$database"
  systemctl start "$service" >/dev/null 2>&1 || true
 fi
 test "$completed" -eq 1 || echo "Primary London directory activation did not complete; prior anchor, bundle and database restored. Backup: $backup" >&2
 exit "$code"
}
trap rollback EXIT HUP INT TERM
systemctl stop "$service"; cp -p "$database" "$backup/events.before.db"; sync "$backup/events.before.db"; changed=1
install -o root -g root -m 0644 "$new_anchors" "$anchors"
install -o root -g root -m 0644 "$new_bundle" "$bundle"
systemctl start "$service"
wait_health(){ attempt=0; until curl -fsS --max-time 2 http://127.0.0.1:3343/healthz >/dev/null; do attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1; done; }
capture_pair(){ prefix=$1; base=$2; "$node" "$collector" "$base" "$memphis" >"$backup/memphis.$prefix.json"; "$node" "$collector" "$base" "$london" >"$backup/london.$prefix.json"; }
wait_health; capture_pair loopback.before-restart ws://127.0.0.1:3343/; capture_pair public.before-restart wss://directory-staging.bitcoinwalk.org/
cmp "$backup/memphis.public.before.json" "$backup/memphis.public.before-restart.json"
systemctl restart "$service"; wait_health; capture_pair loopback.after-restart ws://127.0.0.1:3343/; capture_pair public.after-restart wss://directory-staging.bitcoinwalk.org/
for city in memphis london; do cmp "$backup/$city.loopback.before-restart.json" "$backup/$city.loopback.after-restart.json"; cmp "$backup/$city.loopback.after-restart.json" "$backup/$city.public.after-restart.json"; done
systemctl is-active --quiet "$service"; cp -p "$database" "$backup/events.after.db"
(cd "$backup" && sha256sum anchors.before.json bundle.before.json events.before.db events.after.db memphis.*.json london.*.json >SHA256SUMS)
completed=1; trap - EXIT HUP INT TERM
echo "Primary London directory anchor accepted on 0.8.80. Backup: $backup"
echo "Memphis remained exact and London is public as $london across restart."
echo 'No application, Guide, city relay, replica, DNS or Caddy state was changed.'
