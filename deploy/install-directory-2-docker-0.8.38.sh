#!/bin/sh
# Install the secondary directory transport as an isolated loopback-accessible
# Docker service. Nginx Proxy Manager and DNS are deliberately unchanged.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-2-DOCKER-0.8.38-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-2-staging-0.8.38
dockerfile=./deploy/Dockerfile.directory-2-staging
compose_source=./deploy/compose.directory-2-staging-0.8.38.yaml
capture_source=./deploy/capture-city-directory-root-0.8.37.py
anchors_source=./deploy/city-directory-anchors-0.8.34.json
bundle_source=./deploy/city-directory-memphis-root-0.8.34.json
target=/opt/bitcoinwalk-directory-2-staging
state=/var/lib/bitcoinwalk-directory-2-staging
project=bitcoinwalk-directory-2-staging
container=bitcoinwalk-directory-2-staging
image=bitcoinwalk-directory-2-staging:0.8.38
network=root_my_custom_network
root_event=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
city=be8514a4-9df0-4159-a517-71f65761cbbe

sha256sum -c "$manifest"
test "$(uname -m)" = x86_64 || { echo 'This package requires x86_64.' >&2; exit 1; }
for command in docker python3 curl ss awk grep sort cmp install mktemp sha256sum; do command -v "$command" >/dev/null; done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
docker inspect nginx --format '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' | grep -Fxq "$network"
test "$(docker inspect nginx --format '{{.State.Running}}')" = true
test ! -e "$target" || { echo "$target already exists; stop for review." >&2; exit 1; }
test ! -e "$state" || { echo "$state already exists; stop for review." >&2; exit 1; }
test -z "$(docker ps -aq --filter "name=^/${container}$")" || { echo "$container already exists; stop for review." >&2; exit 1; }
if docker image inspect "$image" >/dev/null 2>&1; then echo "$image already exists; stop for review." >&2; exit 1; fi
if ss -H -lnt | awk '{print $4}' | grep -Eq ':3343$'; then echo 'Host port 3343 is occupied; nothing changed.' >&2; exit 1; fi
available_kb=$(df -Pk /var/lib/docker | awk 'NR==2 {print $4}')
test "$available_kb" -ge 524288 || { echo 'Less than 512 MiB is available; nothing changed.' >&2; exit 1; }

backup=$(mktemp -d /var/backups/bitcoinwalk-directory-2-staging.XXXXXX)
chmod 0700 "$backup"
cp -p /root/docker-compose.yml "$backup/root-docker-compose.yml"
docker inspect nginx >"$backup/nginx-inspect.json"
docker network inspect "$network" >"$backup/network-inspect.json"
docker ps --format '{{.Names}}' | sort >"$backup/containers.before"
docker system df >"$backup/docker-system-df.before"
: >"$backup/target.absent"
: >"$backup/state.absent"
echo "Consistent pre-activation Docker topology backup created: $backup"

changed=0
image_built=0
completed=0
rollback() {
 code=$?; trap - EXIT HUP INT TERM
 if test "$changed" -eq 1; then
  if docker inspect "$container" >/dev/null 2>&1; then
   docker logs "$container" >"$backup/container.log" 2>&1 || true
   docker inspect "$container" >"$backup/container-inspect.failure.json" 2>&1 || true
  fi
  test ! -f "$target/compose.yaml" || docker compose -p "$project" -f "$target/compose.yaml" down >/dev/null 2>&1 || true
  test "$image_built" -eq 0 || docker image rm "$image" >/dev/null 2>&1 || true
  rm -f "$target/bitcoinwalk-relay" "$target/Dockerfile" "$target/compose.yaml" "$target/capture-root.py" "$target/anchors.json" "$target/memphis-bundle.json"
  rmdir "$target" 2>/dev/null || true
  rm -f "$state/events.db"
  rmdir "$state" 2>/dev/null || true
 fi
 if test "$completed" -ne 1; then echo "Secondary directory activation did not complete; created service files were removed. Backup: $backup" >&2; fi
 exit "$code"
}
trap rollback EXIT HUP INT TERM

changed=1
install -d -o root -g root -m 0755 "$target"
install -d -o 65532 -g 65532 -m 0700 "$state"
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile" "$target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$target/compose.yaml"
install -o root -g root -m 0555 "$capture_source" "$target/capture-root.py"
install -o root -g root -m 0444 "$anchors_source" "$target/anchors.json"
install -o root -g root -m 0444 "$bundle_source" "$target/memphis-bundle.json"

docker build --pull=false --network=none -t "$image" "$target"
image_built=1
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build

wait_health() {
 attempt=0
 until curl --fail --silent --max-time 2 http://127.0.0.1:3343/healthz >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 20 || return 1; sleep 1
 done
}
audit_readback() {
 snapshot=$1; report=$2
 python3 "$target/capture-root.py" ws://127.0.0.1:3343/ "$root_event" >"$snapshot"
 chmod 0644 "$snapshot"
 env -i PATH=/usr/bin:/bin \
  RELAY_CITY_DIRECTORY_AUDIT_ANCHORS="$target/anchors.json" \
  RELAY_CITY_DIRECTORY_AUDIT_BUNDLE="$target/memphis-bundle.json" \
  RELAY_CITY_DIRECTORY_AUDIT_MIRRORS="$snapshot" \
  RELAY_CITY_DIRECTORY_AUDIT_CITY="$city" "$target/bitcoinwalk-relay" >"$report"
 grep -q '"bundleVerified": true' "$report"
 grep -q '"attestationCount": 1' "$report"
}

wait_health
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3343/ | grep -q 'BitcoinWalk secondary staging city-directory transport'
audit_readback "$backup/readback-before-restart.json" "$backup/audit-before-restart.json"
docker compose -p "$project" -f "$target/compose.yaml" restart directory >/dev/null
wait_health
audit_readback "$backup/readback-after-restart.json" "$backup/audit-after-restart.json"
cmp "$backup/readback-before-restart.json" "$backup/readback-after-restart.json"
cmp "$backup/audit-before-restart.json" "$backup/audit-after-restart.json"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$container" --format '{{.Config.User}}')" = 65532:65532
test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true
docker logs "$container" >"$backup/container.log" 2>&1
docker inspect "$container" >"$backup/container-inspect.accepted.json"
docker ps --format '{{.Names}}' | grep -vx "$container" | sort >"$backup/containers.after"
cmp "$backup/containers.before" "$backup/containers.after"
docker system df >"$backup/docker-system-df.after"
(
 cd "$backup"
 sha256sum root-docker-compose.yml nginx-inspect.json network-inspect.json containers.before containers.after readback-before-restart.json readback-after-restart.json audit-before-restart.json audit-after-restart.json container.log container-inspect.accepted.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Loopback-only secondary directory Docker transport 0.8.38 accepted. Backup: $backup"
echo "Root event: $root_event"
echo 'The exact signed root and resolved state remained identical across container restart.'
echo 'Nginx Proxy Manager, its Compose project, DNS and every pre-existing container were unchanged.'
