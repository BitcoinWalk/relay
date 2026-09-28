#!/bin/sh
# Install an empty, private-network-only Memphis receiver candidate on the
# secondary VPS. This does not change DNS, Nginx Proxy Manager, the source
# registry, the signed directory chain or any existing receiver.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=REPLICA-CANDIDATE-DOCKER-0.8.43-SHA256SUMS
artifact=./bitcoinwalk-relay-replica-candidate-0.8.43
dockerfile=./deploy/Dockerfile.replica-candidate-0.8.43
compose_source=./deploy/compose.replica-candidate-0.8.43.yaml
target=/opt/bitcoinwalk-replica-candidate
state=/var/lib/bitcoinwalk-replica-candidate
project=bitcoinwalk-replica-candidate
container=bitcoinwalk-replica-candidate
image=bitcoinwalk-replica-candidate:0.8.43
network=root_my_custom_network

sha256sum -c "$manifest"
test "$(uname -m)" = x86_64 || { echo 'This package requires x86_64.' >&2; exit 1; }
for command in docker python3 curl awk grep sort cmp install mktemp sha256sum; do command -v "$command" >/dev/null; done
docker compose version >/dev/null
docker network inspect "$network" >/dev/null
docker inspect nginx --format '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' | grep -Fxq "$network"
test "$(docker inspect nginx --format '{{.State.Running}}')" = true
for path in "$target" "$state"; do test ! -e "$path" || { echo "$path already exists; stop for review." >&2; exit 1; }; done
test -z "$(docker ps -aq --filter "name=^/${container}$")" || { echo "$container already exists; stop for review." >&2; exit 1; }
if docker image inspect "$image" >/dev/null 2>&1; then echo "$image already exists; stop for review." >&2; exit 1; fi
available_kb=$(df -Pk /var/lib/docker | awk 'NR==2 {print $4}')
test "$available_kb" -ge 524288 || { echo 'Less than 512 MiB is available; nothing changed.' >&2; exit 1; }

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-candidate.XXXXXX)
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
rollback(){
 code=$?;trap - EXIT HUP INT TERM
 if test "$changed" -eq 1;then
  if docker inspect "$container" >/dev/null 2>&1;then
   docker logs "$container" >"$backup/container.log" 2>&1||true
   docker inspect "$container" >"$backup/container-inspect.failure.json" 2>&1||true
  fi
  test ! -f "$target/compose.yaml"||docker compose -p "$project" -f "$target/compose.yaml" down >/dev/null 2>&1||true
  test "$image_built" -eq 0||docker image rm "$image" >/dev/null 2>&1||true
  rm -f "$target/bitcoinwalk-relay" "$target/Dockerfile" "$target/compose.yaml"
  rmdir "$target" 2>/dev/null||true
  rm -f "$state/events.db"
  rmdir "$state" 2>/dev/null||true
 fi
 if test "$completed" -ne 1;then echo "Candidate receiver activation did not complete; created files were removed. Backup: $backup" >&2;fi
 exit "$code"
}
trap rollback EXIT HUP INT TERM

changed=1
install -d -o root -g root -m 0755 "$target"
install -d -o 65532 -g 65532 -m 0700 "$state"
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay"
install -o root -g root -m 0444 "$dockerfile" "$target/Dockerfile"
install -o root -g root -m 0444 "$compose_source" "$target/compose.yaml"

docker build --pull=false --network=none -t "$image" "$target"
image_built=1
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build

container_ip(){
 ip=$(docker inspect "$container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}')
 python3 - "$ip" <<'PY'
import ipaddress
import sys

address = ipaddress.ip_address(sys.argv[1])
if address.version != 4 or not address.is_private:
    raise SystemExit("container did not receive a private IPv4 address")
PY
 printf '%s\n' "$ip"
}
wait_health(){
 attempt=0
 until ip=$(container_ip)&&curl --fail --silent --max-time 2 "http://$ip:3344/healthz" >/dev/null;do
  attempt=$((attempt+1));test "$attempt" -lt 20||return 1;sleep 1
 done
}
capture_metadata(){
 output=$1;ip=$(container_ip)
 curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "http://$ip:3344/" >"$output"
 python3 - "$output" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    document = json.load(stream)
if document.get("name") != "BitcoinWalk Memphis production candidate":
    raise SystemExit("unexpected candidate NIP-11 relay name")
if document.get("version") != "bitcoinwalk-replica-candidate-0.8.43":
    raise SystemExit("unexpected candidate NIP-11 relay version")
PY
}

wait_health
container_ip >"$backup/container-ip.before"
ip=$(cat "$backup/container-ip.before")
curl --fail --silent --show-error --max-time 5 "http://$ip:3344/" >"$backup/landing.txt"
grep -q 'Not yet present in the signed city directory or live replication registry' "$backup/landing.txt"
capture_metadata "$backup/nip11.before.json"
docker compose -p "$project" -f "$target/compose.yaml" restart receiver >/dev/null
wait_health
container_ip >"$backup/container-ip.after"
cmp "$backup/container-ip.before" "$backup/container-ip.after"
capture_metadata "$backup/nip11.after.json"
cmp "$backup/nip11.before.json" "$backup/nip11.after.json"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$container" --format '{{.Config.User}}')" = 65532:65532
test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true
test -z "$(docker port "$container")"
docker logs "$container" >"$backup/container.log" 2>&1
grep -q 'Replica WSS receiver enabled for one operator-configured city; service key a6c0c0aba1385505e1e1db704934a67d93fac6c5561d8f256e6dcac1df2ed448' "$backup/container.log"
docker inspect "$container" >"$backup/container-inspect.accepted.json"
docker ps --format '{{.Names}}' | grep -vx "$container" | sort >"$backup/containers.after"
cmp "$backup/containers.before" "$backup/containers.after"
docker system df >"$backup/docker-system-df.after"
(
 cd "$backup"
 sha256sum root-docker-compose.yml nginx-inspect.json network-inspect.json containers.before containers.after container-ip.before container-ip.after landing.txt nip11.before.json nip11.after.json container.log container-inspect.accepted.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Private-network-only Memphis candidate receiver 0.8.43 accepted. Backup: $backup"
echo 'Candidate: wss://replica.bitcoinwalk.org/'
echo 'The receiver is empty, isolated and restart-stable; no host port is published.'
echo 'DNS, Nginx Proxy Manager, the live replication registry, the signed directory chain and every pre-existing container were unchanged.'
