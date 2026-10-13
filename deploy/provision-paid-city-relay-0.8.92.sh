#!/bin/sh
# Idempotently create one isolated, non-root Khatru paid-city receiver and add
# its exact hostname to the fail-closed gateway. The supplied manifest is
# produced only after exact approval and paid-entitlement verification.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on the paid-relay host.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /absolute/path/to/city-relay-manifest.json" >&2; exit 1; }
cd "$(dirname "$0")/.."

case "$1" in /*) request=$1;; *) echo 'Manifest path must be absolute.' >&2; exit 1;; esac
test "$(readlink -f "$request")" = "$request" || { echo 'Manifest path must be clean and contain no symlink.' >&2; exit 1; }
test -f "$request" && test ! -L "$request"
manifest=CITY-RELAY-PROVISIONING-0.8.92-SHA256SUMS
artifact=./bitcoinwalk-relay-city-0.8.92
audit=./replica-audit-city-0.8.92
dockerfile=./deploy/Dockerfile.city-relay-0.8.92
network=root_my_custom_network
gateway=bitcoinwalk-city-relay-gateway
gateway_registry=/var/lib/bitcoinwalk-city-relay-gateway/registry.json
service_pubkey=a6c0c0aba1385505e1e1db704934a67d93fac6c5561d8f256e6dcac1df2ed448

sha256sum --ignore-missing -c "$manifest"
test -x "$artifact" && test -x "$audit"
for command in docker python3 curl grep install mktemp sha256sum flock; do command -v "$command" >/dev/null; done
docker compose version >/dev/null
test "$(docker inspect "$gateway" --format '{{.State.Running}}')" = true
test -f "$gateway_registry"

values=$(python3 - "$request" "$service_pubkey" <<'PY'
import json,re,sys,uuid
path,service=sys.argv[1:]
with open(path,encoding="utf-8") as handle: data=json.load(handle)
expected={"version","cityId","slug","cityName","destination","approvalEventId","entitlementId","evidenceDigest","replicationServicePubkey"}
if set(data)!=expected or data["version"]!=1: raise SystemExit("invalid city relay manifest schema")
try: city=str(uuid.UUID(data["cityId"]));
except Exception: raise SystemExit("invalid city ID")
slug=data["slug"]
if not isinstance(slug,str) or len(slug)>63 or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*",slug): raise SystemExit("invalid city slug")
name=data["cityName"]
if not isinstance(name,str) or not name.strip() or len(name)>100 or any(ord(c)<32 for c in name): raise SystemExit("invalid city name")
destination=f"wss://{slug}.bitcoinwalk.org/"
if data["destination"]!=destination or data["replicationServicePubkey"]!=service: raise SystemExit("invalid relay scope")
for field in ("approvalEventId","entitlementId","evidenceDigest"):
 if not isinstance(data[field],str) or not re.fullmatch(r"[0-9a-f]{64}",data[field]): raise SystemExit("invalid evidence commitment")
for value in (city,slug,name.strip(),destination,data["approvalEventId"],data["entitlementId"],data["evidenceDigest"]):
 print(value)
PY
)
city=$(printf '%s\n' "$values" | sed -n '1p')
slug=$(printf '%s\n' "$values" | sed -n '2p')
city_name=$(printf '%s\n' "$values" | sed -n '3p')
destination=$(printf '%s\n' "$values" | sed -n '4p')
approval=$(printf '%s\n' "$values" | sed -n '5p')
entitlement=$(printf '%s\n' "$values" | sed -n '6p')
evidence=$(printf '%s\n' "$values" | sed -n '7p')
container=bitcoinwalk-relay-$slug
project=bitcoinwalk-relay-$slug
image=bitcoinwalk-city-relay:0.8.92
target=/opt/bitcoinwalk-city-relays/$slug
state=/var/lib/bitcoinwalk-city-relays/$slug
request_digest=$(sha256sum "$request" | cut -d ' ' -f 1)

container_ip(){ docker inspect "$container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}'; }
wait_health(){
  ready=false
  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    ip=$(container_ip 2>/dev/null || true)
    if test -n "$ip" && curl --fail --silent --max-time 2 "http://$ip:3345/healthz" >/dev/null; then ready=true; break; fi
    sleep 1
  done
  test "$ready" = true
}
verify_exact(){
  test -f "$target/manifest.sha256" && grep -Fxq "$request_digest" "$target/manifest.sha256"
  test "$(docker inspect "$container" --format '{{.Config.User}}')" = 65532:65532
  test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true
  test -z "$(docker port "$container")"
  wait_health
  ip=$(container_ip)
  "$audit" -source "ws://$ip:3345" -replica "ws://$ip:3345" -city "$city" -allow-empty >/dev/null
  gateway_ip=$(docker inspect "$gateway" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}')
  curl --fail --silent --show-error --max-time 5 -H "Host: $slug.bitcoinwalk.org" -H 'Accept: application/nostr+json' "http://$gateway_ip:8080/" | grep -Fq "BitcoinWalk $city_name relay"
}

if docker inspect "$container" >/dev/null 2>&1; then
  verify_exact
  docker compose -p "$project" -f "$target/compose.yaml" restart relay >/dev/null
  verify_exact
  echo "Paid-city relay $destination is already provisioned with the exact manifest and passed restart checks."
  exit 0
fi
for path in "$target" "$state"; do test ! -e "$path" || { echo "$path exists without the accepted container; stop for review." >&2; exit 1; }; done

backup=$(mktemp -d /var/backups/bitcoinwalk-city-relay-$slug.XXXXXX)
chmod 0700 "$backup"
cp -p "$gateway_registry" "$backup/gateway-registry.before.json"
docker ps --format '{{.Names}}' | sort >"$backup/containers.before"
changed=0
completed=0
rollback(){
  code=$?; trap - EXIT HUP INT TERM
  if test "$changed" -eq 1; then
    cp -p "$backup/gateway-registry.before.json" "$gateway_registry" || true
    test ! -f "$target/compose.yaml" || docker compose -p "$project" -f "$target/compose.yaml" down >/dev/null 2>&1 || true
    rm -f "$target/compose.yaml" "$target/manifest.json" "$target/manifest.sha256" "$state/events.db"
    rmdir "$target" "$state" 2>/dev/null || true
  fi
  test "$completed" -eq 1 || echo "Paid-city relay provisioning did not complete; created route and container were removed. Backup: $backup" >&2
  exit "$code"
}
trap rollback EXIT HUP INT TERM

changed=1
install -d -o root -g root -m 0755 /opt/bitcoinwalk-city-relays "$target"
install -d -o 65532 -g 65532 -m 0700 /var/lib/bitcoinwalk-city-relays "$state"
install -o root -g root -m 0444 "$request" "$target/manifest.json"
printf '%s\n' "$request_digest" >"$target/manifest.sha256"
chmod 0444 "$target/manifest.sha256"
python3 - "$target/compose.yaml" "$slug" "$city" "$city_name" "$destination" "$service_pubkey" <<'PY'
import json,sys
path,slug,city,name,destination,service=sys.argv[1:]
q=lambda value: json.dumps(value,ensure_ascii=False)
text=f'''services:
  relay:
    image: bitcoinwalk-city-relay:0.8.92
    container_name: bitcoinwalk-relay-{slug}
    init: true
    restart: unless-stopped
    read_only: true
    user: "65532:65532"
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    pids_limit: 64
    mem_limit: 512m
    stop_grace_period: 20s
    environment:
      RELAY_LISTEN: 0.0.0.0:3345
      RELAY_REPLICA_RECEIVER_CONTAINER_LISTEN: "true"
      RELAY_DB: /data/events.db
      RELAY_ORGANIZER_MODE: "true"
      RELAY_REPLICA_RECEIVER_CITY: {q(city)}
      RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY: {q(service)}
      RELAY_REPLICA_RECEIVER_DESTINATION: {q(destination)}
      RELAY_NAME: {q('BitcoinWalk '+name+' relay')}
      RELAY_VERSION: bitcoinwalk-city-relay-0.8.92
      RELAY_DESCRIPTION: {q('Dedicated '+name+' city receiver. Public reads; authenticated city-scoped replication writes only.')}
      RELAY_LANDING_TEXT: {q('BitcoinWalk '+name+' city relay.')}
    volumes:
      - /var/lib/bitcoinwalk-city-relays/{slug}:/data
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=32m,mode=0700,uid=65532,gid=65532
    expose: ["3345"]
    networks: [proxy]
networks:
  proxy:
    external: true
    name: root_my_custom_network
'''
open(path,"x",encoding="utf-8").write(text)
PY
chmod 0444 "$target/compose.yaml"

if ! docker image inspect "$image" >/dev/null 2>&1; then
  build=$(mktemp -d "$backup/build.XXXXXX")
  install -m 0555 "$artifact" "$build/bitcoinwalk-relay"
  install -m 0444 "$dockerfile" "$build/Dockerfile"
  docker build --pull=false --network=none -t "$image" "$build"
fi
docker compose -p "$project" -f "$target/compose.yaml" up -d --no-build
wait_health
ip=$(container_ip)
"$audit" -source "ws://$ip:3345" -replica "ws://$ip:3345" -city "$city" -allow-empty >"$backup/empty.before.json"

flock "$gateway_registry.lock" python3 - "$gateway_registry" "$city" "$slug" <<'PY'
import json,os,sys,tempfile
path,city,slug=sys.argv[1:]
with open(path,encoding="utf-8") as handle: data=json.load(handle)
if set(data)!={"version","routes"} or data["version"]!=1 or not isinstance(data["routes"],list): raise SystemExit("invalid gateway registry")
route={"cityId":city,"slug":slug,"host":slug+".bitcoinwalk.org","upstream":"http://bitcoinwalk-relay-"+slug+":3345"}
matches=[row for row in data["routes"] if row.get("cityId")==city or row.get("slug")==slug or row.get("host")==route["host"]]
if matches and matches!=[route]: raise SystemExit("gateway route conflicts with existing city")
if not matches: data["routes"].append(route)
data["routes"].sort(key=lambda row: row["host"])
directory=os.path.dirname(path)
fd,tmp=tempfile.mkstemp(prefix="registry.",dir=directory,text=True)
try:
 with os.fdopen(fd,"w",encoding="utf-8") as handle: json.dump(data,handle,indent=2,ensure_ascii=False);handle.write("\n");handle.flush();os.fsync(handle.fileno())
 os.chmod(tmp,0o640);os.chown(tmp,65532,65532);os.replace(tmp,path)
finally:
 if os.path.exists(tmp): os.unlink(tmp)
PY

verify_exact
docker compose -p "$project" -f "$target/compose.yaml" restart relay >/dev/null
verify_exact
ip=$(container_ip)
"$audit" -source "ws://$ip:3345" -replica "ws://$ip:3345" -city "$city" -allow-empty >"$backup/empty.after.json"
cmp "$backup/empty.before.json" "$backup/empty.after.json"
cp -p "$gateway_registry" "$backup/gateway-registry.after.json"
docker inspect "$container" >"$backup/container.accepted.json"
docker ps --format '{{.Names}}' | grep -vx "$container" | sort >"$backup/containers.after"
cmp "$backup/containers.before" "$backup/containers.after"
printf 'cityId=%s\nslug=%s\ndestination=%s\napprovalEventId=%s\nentitlementId=%s\nevidenceDigest=%s\n' "$city" "$slug" "$destination" "$approval" "$entitlement" "$evidence" >"$backup/accepted-scope.txt"
(
  cd "$backup"
  sha256sum gateway-registry.before.json gateway-registry.after.json containers.before containers.after empty.before.json empty.after.json container.accepted.json accepted-scope.txt >SHA256SUMS
)
completed=1
trap - EXIT HUP INT TERM
echo "Isolated paid-city relay provisioned on 0.8.92. Evidence: $backup"
echo "Endpoint: $destination"
echo 'The container is non-root, read-only, private-network-only, empty and restart-stable.'
echo 'Public DNS/TLS/WSS, source replication and owner-directory activation remain independent readiness gates.'
