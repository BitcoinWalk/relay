#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run as root on .240.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-ACTIVATION-0.8.84-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-activation-0.8.84
compose_source=./deploy/compose.directory-2-production-0.8.84.yaml
dockerfile_source=./deploy/Dockerfile.directory-2-staging
collector=./deploy/capture-city-directory-root-0.8.39.py
target=/opt/bitcoinwalk-directory-2-staging
database=/var/lib/bitcoinwalk-directory-2-staging/events.db
compose="$target/compose.yaml"
container=bitcoinwalk-directory-2-staging
project=bitcoinwalk-directory-2-staging
old_image=bitcoinwalk-directory-2-staging:0.8.79
image=bitcoinwalk-directory-2-staging:0.8.84
memphis=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
london=d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2
predecessor=014c3c9529c3272f8a9cef2fc01abae647988e2a00b8324c1c08b6480697a721

sha256sum -c "$manifest"
test "$(sha256sum "$target/bitcoinwalk-relay" | cut -d ' ' -f 1)" = "$predecessor" || { echo 'Installed secondary directory binary is not the accepted 0.8.79 predecessor.' >&2; exit 1; }
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
test "$(docker inspect "$container" --format '{{.Config.Image}}')" = "$old_image"
test -z "$(docker port "$container")"
backup=$(mktemp -d /var/backups/bitcoinwalk-directory-activation-secondary.XXXXXX);chmod 0700 "$backup"
cp -p "$target/bitcoinwalk-relay" "$backup/bitcoinwalk-relay.before";cp -p "$compose" "$backup/compose.before.yaml"
python3 "$collector" wss://directory-2.bitcoinwalk.org/ "$memphis" >"$backup/memphis.before.json";python3 "$collector" wss://directory-2.bitcoinwalk.org/ "$london" >"$backup/london.before.json"
docker inspect "$container" >"$backup/container.before.json"

changed=0;image_built=0;completed=0
restore(){ code=$?;trap - EXIT HUP INT TERM;if test "$changed" -eq 1;then docker compose -p "$project" -f "$compose" down >/dev/null 2>&1||true;install -o root -g root -m 0555 "$backup/bitcoinwalk-relay.before" "$target/bitcoinwalk-relay";install -o root -g root -m 0444 "$backup/compose.before.yaml" "$compose";install -o 65532 -g 65532 -m 0600 "$backup/events.db" "$database";docker compose -p "$project" -f "$compose" up -d --no-build --force-recreate >/dev/null 2>&1||true;fi;test "$image_built" -eq 0||docker image rm "$image" >/dev/null 2>&1||true;test "$completed" -eq 1||echo "Secondary directory activation-policy upgrade did not complete; accepted container restored. Backup: $backup" >&2;exit "$code";}
trap restore EXIT HUP INT TERM
docker stop -t 20 "$container" >/dev/null;cp -p "$database" "$backup/events.db";sync "$backup/events.db";changed=1
install -o root -g root -m 0555 "$artifact" "$target/bitcoinwalk-relay";install -o root -g root -m 0444 "$dockerfile_source" "$target/Dockerfile";install -o root -g root -m 0444 "$compose_source" "$compose"
docker build --pull=false --network=none -t "$image" "$target";image_built=1;docker compose -p "$project" -f "$compose" up -d --no-build --force-recreate >/dev/null
container_ip(){ docker inspect "$container" --format '{{with index .NetworkSettings.Networks "root_my_custom_network"}}{{.IPAddress}}{{end}}'; }
attempt=0;until ip=$(container_ip)&&curl -fsS --max-time 2 "http://$ip:3343/healthz" >/dev/null;do attempt=$((attempt+1));test "$attempt" -lt 30;sleep 1;done
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' "http://$ip:3343/" >"$backup/nip11.after.json";grep -Fq 'bitcoinwalk-directory-transport-0.8.84' "$backup/nip11.after.json"
python3 "$collector" wss://directory-2.bitcoinwalk.org/ "$memphis" >"$backup/memphis.after.json";python3 "$collector" wss://directory-2.bitcoinwalk.org/ "$london" >"$backup/london.after.json"
cmp "$backup/memphis.before.json" "$backup/memphis.after.json";cmp "$backup/london.before.json" "$backup/london.after.json"
docker compose -p "$project" -f "$compose" restart directory >/dev/null;attempt=0;until ip=$(container_ip)&&curl -fsS --max-time 2 "http://$ip:3343/healthz" >/dev/null;do attempt=$((attempt+1));test "$attempt" -lt 30;sleep 1;done
python3 "$collector" wss://directory-2.bitcoinwalk.org/ "$memphis" >"$backup/memphis.after-restart.json";python3 "$collector" wss://directory-2.bitcoinwalk.org/ "$london" >"$backup/london.after-restart.json"
cmp "$backup/memphis.after.json" "$backup/memphis.after-restart.json";cmp "$backup/london.after.json" "$backup/london.after-restart.json"
docker inspect "$container" >"$backup/container.after.json";test "$(docker inspect "$container" --format '{{.Config.User}}')" = 65532:65532;test "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = true;test -z "$(docker port "$container")"
(cd "$backup" && sha256sum ./* >SHA256SUMS)
completed=1;trap - EXIT HUP INT TERM
echo "Secondary directory operator-transport policy accepted on 0.8.84. Backup: $backup"
echo 'Owner signatures and pinned-chain validation remain mandatory; a current endpoint operator may authenticate transport only.'
echo 'Both owner-signed roots remained byte-identical across container recreation and restart.'
