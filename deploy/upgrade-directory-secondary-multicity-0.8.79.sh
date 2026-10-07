#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-MULTICITY-0.8.79-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-multicity-0.8.79
dockerfile_source=./deploy/Dockerfile.directory-2-staging
compose_source=./deploy/compose.directory-2-staging-0.8.79.yaml
collector=./deploy/capture-city-directory-root-0.8.39.py
target=/opt/bitcoinwalk-directory-2-staging
database=/var/lib/bitcoinwalk-directory-2-staging/events.db
container=bitcoinwalk-directory-2-staging
project=bitcoinwalk-directory-2-staging
old_image=bitcoinwalk-directory-2-staging:0.8.41
image=bitcoinwalk-directory-2-staging:0.8.79
root_event=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
old_sha=9c27118b11674d05fd9f46d94b53685e0fb0d6220c3d83a63e5fd62c0af8f646

sha256sum -c "$manifest"
test "$(sha256sum "$target/bitcoinwalk-relay" | cut -d ' ' -f 1)" = "$old_sha" || { echo 'Installed secondary directory binary is not the accepted predecessor.' >&2; exit 1; }
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$container" --format '{{.Config.Image}}')" = "$old_image"
test -z "$(docker port "$container")"
docker image inspect "$old_image" >/dev/null
if docker image inspect "$image" >/dev/null 2>&1; then echo 'Target image already exists; stop for review.' >&2; exit 1; fi

backup=$(mktemp -d /var/backups/bitcoinwalk-directory-multicity-secondary.XXXXXX)
chmod 0700 "$backup"
cp -p "$target/bitcoinwalk-relay" "$backup/bitcoinwalk-relay.before"
cp -p "$target/Dockerfile" "$backup/Dockerfile.before"
cp -p "$target/compose.yaml" "$backup/compose.before.yaml"
cp -p "$target/anchors.json" "$backup/anchors.json"
cp -p "$target/memphis-bundle.json" "$backup/memphis-bundle.json"
python3 "$collector" wss://directory-2-staging.bitcoinwalk.org/ "$root_event" >"$backup/public.before.json"
docker inspect "$container" >"$backup/container.before.json"

changed=0
image_built=0
completed=0
rollback(){
 code=$?; trap - EXIT HUP INT TERM
 if test "$changed" -eq 1; then
  docker compose -p "$project" -f "$target/compose.yaml" down >/dev/null 2>&1 || true
  install -o root -g root -m 0555 "$backup/bitcoinwalk-relay.before" "$target/bitcoinwalk-relay"
  install -o root -g root -m 0444 "$backup/Dockerfile.before" "$target/Dockerfile"
  install -o root -g root -m 0444 "$backup/compose.before.yaml" "$target/compose.yaml"
  install -o 65532 -g 65532 -m 0600 "$backup/events.db" "$database"
  docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null 2>&1 || true
 fi
 test "$image_built" -eq 0 || docker image rm "$image" >/dev/null 2>&1 || true
 test "$completed" -eq 1 || echo "Secondary multi-city upgrade did not complete; accepted container restored. Backup: $backup" >&2
 exit "$code"
}
trap rollback EXIT HUP INT TERM

docker stop -t 20 "$container" >/dev/null
cp -p "$database" "$backup/events.db"
sync "$backup/events.db"
changed=1
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile_source" "$target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$target/compose.yaml"
docker build --pull=false --network=none -t "$image" "$target"
image_built=1
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build --force-recreate >/dev/null

container_ip(){ docker inspect "$container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}'; }
wait_health(){
 attempt=0
 until ip=$(container_ip) && curl --fail --silent --max-time 2 "http://$ip:3343/healthz" >/dev/null; do attempt=$((attempt+1)); test "$attempt" -lt 30 || return 1; sleep 1; done
}
capture_private(){ ip=$(container_ip); python3 "$collector" "ws://$ip:3343/" "$root_event" --allow-private-ws >"$1"; }
wait_health
ip=$(container_ip)
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "http://$ip:3343/" >"$backup/nip11.json"
grep -q 'bitcoinwalk-directory-transport-0.8.79' "$backup/nip11.json"
capture_private "$backup/private.before-restart.json"
docker compose -p "$project" -f "$target/compose.yaml" restart directory >/dev/null
wait_health
capture_private "$backup/private.after-restart.json"
python3 "$collector" wss://directory-2-staging.bitcoinwalk.org/ "$root_event" >"$backup/public.after.json"
cmp "$backup/public.before.json" "$backup/public.after.json"
cmp "$backup/private.before-restart.json" "$backup/private.after-restart.json"
cmp "$backup/public.after.json" "$backup/private.after-restart.json"
docker inspect "$container" >"$backup/container.after.json"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$container" --format '{{.Config.User}}')" = 65532:65532
test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true
test -z "$(docker port "$container")"
(
 cd "$backup"
 sha256sum bitcoinwalk-relay.before Dockerfile.before compose.before.yaml anchors.json memphis-bundle.json events.db public.before.json public.after.json private.before-restart.json private.after-restart.json nip11.json container.before.json container.after.json >SHA256SUMS
)
completed=1
trap - EXIT HUP INT TERM
echo "Secondary multi-city-capable directory transport 0.8.79 accepted. Backup: $backup"
echo "Memphis root remained exact across activation and restart: $root_event"
echo 'The container remains non-root, read-only, private-network-only and without a host-published port.'
echo 'No anchor, signed event, DNS, Nginx Proxy Manager or pre-existing container was changed.'
