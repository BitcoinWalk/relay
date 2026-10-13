#!/bin/sh
# Install the fail-closed wildcard gateway used by automatic paid-city relay
# provisioning. Nginx Proxy Manager remains a one-time operator boundary.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on the paid-relay host.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=CITY-RELAY-PROVISIONING-0.8.92-SHA256SUMS
artifact=./bitcoinwalk-relay-city-0.8.92
dockerfile=./deploy/Dockerfile.city-relay-0.8.92
compose_source=./deploy/compose.city-relay-gateway-0.8.92.yaml
target=/opt/bitcoinwalk-city-relay-gateway
state=/var/lib/bitcoinwalk-city-relay-gateway
registry=$state/registry.json
project=bitcoinwalk-city-relay-gateway
container=bitcoinwalk-city-relay-gateway
image=bitcoinwalk-city-relay-gateway:0.8.92
network=root_my_custom_network

sha256sum --ignore-missing -c "$manifest"
test -x "$artifact"
test "$(uname -m)" = x86_64 || { echo 'This package requires x86_64.' >&2; exit 1; }
for command in docker python3 curl grep install mktemp sha256sum; do command -v "$command" >/dev/null; done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
docker inspect nginx --format '{{json .NetworkSettings.Networks}}' | grep -q "$network"
test "$(docker inspect nginx --format '{{.State.Running}}')" = true

if docker inspect "$container" >/dev/null 2>&1; then
  test -f "$target/compose.yaml" && test -f "$registry"
  test "$(docker inspect "$container" --format '{{.Config.User}}')" = 65532:65532
  test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true
  ip=$(docker inspect "$container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}')
  curl --fail --silent --show-error --max-time 5 -H 'Host: gateway.internal' "http://$ip:8080/__bitcoinwalk_gateway_healthz" >/dev/null
  echo 'City relay gateway is already installed and healthy; no state changed.'
  exit 0
fi
for path in "$target" "$state"; do test ! -e "$path" || { echo "$path already exists without the accepted gateway; stop for review." >&2; exit 1; }; done
if docker image inspect "$image" >/dev/null 2>&1; then echo "$image already exists without the accepted gateway; stop for review." >&2; exit 1; fi

backup=$(mktemp -d /var/backups/bitcoinwalk-city-relay-gateway.XXXXXX)
chmod 0700 "$backup"
docker inspect nginx >"$backup/nginx.before.json"
docker network inspect "$network" >"$backup/network.before.json"
docker ps --format '{{.Names}}' | sort >"$backup/containers.before"
changed=0
completed=0
rollback(){
  code=$?; trap - EXIT HUP INT TERM
  if test "$changed" -eq 1; then
    test ! -f "$target/compose.yaml" || docker compose -p "$project" -f "$target/compose.yaml" down >/dev/null 2>&1 || true
    docker image rm "$image" >/dev/null 2>&1 || true
    rm -f "$target/bitcoinwalk-relay" "$target/Dockerfile" "$target/compose.yaml" "$state/registry.json"
    rmdir "$target" "$state" 2>/dev/null || true
  fi
  test "$completed" -eq 1 || echo "City relay gateway installation did not complete. Backup: $backup" >&2
  exit "$code"
}
trap rollback EXIT HUP INT TERM

changed=1
install -d -o root -g root -m 0755 "$target"
install -d -o 65532 -g 65532 -m 0750 "$state"
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile" "$target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$target/compose.yaml"
printf '{\n  "version": 1,\n  "routes": []\n}\n' >"$registry"
chown 65532:65532 "$registry"
chmod 0640 "$registry"
docker build --pull=false --network=none -t "$image" "$target"
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build

ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  ip=$(docker inspect "$container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}')
  if test -n "$ip" && curl --fail --silent --max-time 2 -H 'Host: gateway.internal' "http://$ip:8080/__bitcoinwalk_gateway_healthz" >/dev/null; then ready=true; break; fi
  sleep 1
done
test "$ready" = true
test -z "$(docker port "$container")"
test "$(docker inspect "$container" --format '{{.Config.User}}')" = 65532:65532
test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true
test -n "$(docker inspect "$container" --format '{{index .HostConfig.Tmpfs "/tmp"}}')"
docker compose -p "$project" -f "$target/compose.yaml" restart gateway >/dev/null
sleep 1
ip=$(docker inspect "$container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}')
curl --fail --silent --show-error --max-time 5 -H 'Host: gateway.internal' "http://$ip:8080/__bitcoinwalk_gateway_healthz" >"$backup/health.after.json"
docker inspect "$container" >"$backup/gateway.accepted.json"
docker ps --format '{{.Names}}' | grep -vx "$container" | sort >"$backup/containers.after"
cmp "$backup/containers.before" "$backup/containers.after"
(
  cd "$backup"
  sha256sum nginx.before.json network.before.json containers.before containers.after health.after.json gateway.accepted.json >SHA256SUMS
)
completed=1
trap - EXIT HUP INT TERM
echo "Fail-closed city relay gateway accepted on 0.8.92. Backup: $backup"
echo 'One-time operator step: point the existing *.bitcoinwalk.org Nginx Proxy Manager host to bitcoinwalk-city-relay-gateway:8080 with WebSocket support.'
echo 'Unknown subdomains return 404; only exact provisioned city routes are forwarded.'
