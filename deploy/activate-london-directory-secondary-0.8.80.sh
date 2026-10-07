#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-LONDON-ACTIVATION-0.8.80-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-multicity-0.8.79
collector=./deploy/capture-city-directory-root-0.8.39.py
old_anchors=./deploy/city-directory-anchors-0.8.34.json
old_bundle=./deploy/city-directory-memphis-root-0.8.34.json
new_anchors=./deploy/city-directory-anchors-0.8.80.json
new_bundle=./deploy/city-directory-memphis-london-bundle-0.8.80.json
target=/opt/bitcoinwalk-directory-2-staging
anchors=$target/anchors.json
bundle=$target/memphis-bundle.json
database=/var/lib/bitcoinwalk-directory-2-staging/events.db
container=bitcoinwalk-directory-2-staging
project=bitcoinwalk-directory-2-staging
compose=$target/compose.yaml
memphis=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
london=d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2

sha256sum -c "$manifest"
cmp -s "$anchors" "$old_anchors"; cmp -s "$bundle" "$old_bundle"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$container" --format '{{.Config.Image}}')" = bitcoinwalk-directory-2-staging:0.8.79
test -z "$(docker port "$container")"
for city in be8514a4-9df0-4159-a517-71f65761cbbe ca2f9905-fb4d-4948-a12c-c792b28ec7c8; do
 RELAY_CITY_DIRECTORY_AUDIT_ANCHORS="$PWD/$new_anchors" RELAY_CITY_DIRECTORY_AUDIT_BUNDLE="$PWD/$new_bundle" RELAY_CITY_DIRECTORY_AUDIT_CITY="$city" "$artifact" >/dev/null
done
backup=$(mktemp -d /var/backups/bitcoinwalk-directory-london-secondary.XXXXXX); chmod 0700 "$backup"
cp -p "$anchors" "$backup/anchors.before.json"; cp -p "$bundle" "$backup/bundle.before.json"; docker inspect "$container" >"$backup/container.before.json"
python3 "$collector" wss://directory-2-staging.bitcoinwalk.org/ "$memphis" >"$backup/memphis.public.before.json"
if python3 "$collector" wss://directory-2-staging.bitcoinwalk.org/ "$london" >"$backup/london.unexpected.before.json" 2>/dev/null; then echo 'London root is already public; stop for audit.' >&2; exit 1; fi

changed=0; completed=0
rollback(){
 code=$?; trap - EXIT HUP INT TERM
 if test "$changed" -eq 1; then
  docker compose -p "$project" -f "$compose" down >/dev/null 2>&1 || true
  install -o root -g root -m 0444 "$backup/anchors.before.json" "$anchors"
  install -o root -g root -m 0444 "$backup/bundle.before.json" "$bundle"
  install -o 65532 -g 65532 -m 0600 "$backup/events.before.db" "$database"
  docker compose -p "$project" -f "$compose" up -d --no-build --force-recreate >/dev/null 2>&1 || true
 fi
 test "$completed" -eq 1 || echo "Secondary London directory activation did not complete; prior anchor, bundle and database restored. Backup: $backup" >&2
 exit "$code"
}
trap rollback EXIT HUP INT TERM
docker stop -t 20 "$container" >/dev/null; cp -p "$database" "$backup/events.before.db"; sync "$backup/events.before.db"; changed=1
install -o root -g root -m 0444 "$new_anchors" "$anchors"
install -o root -g root -m 0444 "$new_bundle" "$bundle"
docker compose -p "$project" -f "$compose" up -d --no-build --force-recreate >/dev/null
container_ip(){ docker inspect "$container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}'; }
wait_health(){ attempt=0; until ip=$(container_ip) && curl -fsS --max-time 2 "http://$ip:3343/healthz" >/dev/null; do attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1; done; }
capture_private(){ prefix=$1; ip=$(container_ip); python3 "$collector" "ws://$ip:3343/" "$memphis" --allow-private-ws >"$backup/memphis.$prefix.json"; python3 "$collector" "ws://$ip:3343/" "$london" --allow-private-ws >"$backup/london.$prefix.json"; }
capture_public(){ prefix=$1; python3 "$collector" wss://directory-2-staging.bitcoinwalk.org/ "$memphis" >"$backup/memphis.$prefix.json"; python3 "$collector" wss://directory-2-staging.bitcoinwalk.org/ "$london" >"$backup/london.$prefix.json"; }
wait_health; capture_private private.before-restart; capture_public public.before-restart
cmp "$backup/memphis.public.before.json" "$backup/memphis.public.before-restart.json"
docker compose -p "$project" -f "$compose" restart directory >/dev/null; wait_health; capture_private private.after-restart; capture_public public.after-restart
for city in memphis london; do cmp "$backup/$city.private.before-restart.json" "$backup/$city.private.after-restart.json"; cmp "$backup/$city.private.after-restart.json" "$backup/$city.public.after-restart.json"; done
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$container" --format '{{.Config.User}}')" = 65532:65532
test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true
test -z "$(docker port "$container")"; cp -p "$database" "$backup/events.after.db"; docker inspect "$container" >"$backup/container.after.json"
(cd "$backup" && sha256sum anchors.before.json bundle.before.json events.before.db events.after.db container.before.json container.after.json memphis.*.json london.*.json >SHA256SUMS)
completed=1; trap - EXIT HUP INT TERM
echo "Secondary London directory anchor accepted on 0.8.80. Backup: $backup"
echo "Memphis remained exact and London is public as $london across restart."
echo 'The container remains non-root, read-only, private-network-only and without a host-published port.'
echo 'No application, Guide, city relay, replica, DNS or Nginx Proxy Manager state was changed.'
