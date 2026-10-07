#!/bin/sh
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on .240.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

compose_source=./deploy/compose.directory-2-production-0.8.83.yaml
compose=/opt/bitcoinwalk-directory-2-staging/compose.yaml
project=bitcoinwalk-directory-2-staging
container=bitcoinwalk-directory-2-staging
collector=./deploy/capture-city-directory-root-0.8.39.py
python=python3
command -v "$python" >/dev/null
memphis=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
london=d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2
endpoint_wss=wss://directory-2.bitcoinwalk.org/
endpoint_https=https://directory-2.bitcoinwalk.org/

sha256sum -c DIRECTORY-PRODUCTION-METADATA-0.8.83-SHA256SUMS
docker inspect "$container" >/dev/null
docker image inspect bitcoinwalk-directory-2-staging:0.8.79 >/dev/null
backup=$(mktemp -d /var/backups/bitcoinwalk-directory-production-metadata-secondary.XXXXXX)
chmod 0700 "$backup"
docker inspect "$container" >"$backup/container.before.json"
docker logs "$container" >"$backup/container.before.log" 2>&1
cp -p /root/docker-compose.yml "$backup/root-docker-compose.yml"
cp -p "$compose" "$backup/compose.before.yaml"
"$python" "$collector" "$endpoint_wss" "$memphis" >"$backup/memphis.before.json"
"$python" "$collector" "$endpoint_wss" "$london" >"$backup/london.before.json"

completed=0
restore() {
  code=$?
  trap - EXIT HUP INT TERM
  if test "$completed" -ne 1; then
    install -o root -g root -m 0644 "$backup/compose.before.yaml" "$compose"
    docker compose -p "$project" -f "$compose" up -d --force-recreate >/dev/null 2>&1 || true
    echo "Secondary directory metadata activation did not complete; prior Compose configuration restored. Backup: $backup" >&2
  fi
  exit "$code"
}
trap restore EXIT HUP INT TERM

install -o root -g root -m 0644 "$compose_source" "$compose"
docker compose -p "$project" -f "$compose" up -d --force-recreate
attempt=0
until curl -fsS --max-time 2 "${endpoint_https}healthz" >/dev/null; do
  attempt=$((attempt + 1)); test "$attempt" -lt 30; sleep 1
done
curl -fsS --max-time 10 -H 'Accept: application/nostr+json' "$endpoint_https" >"$backup/nip11.after.json"
grep -Fq '"name":"BitcoinWalk production directory relay"' "$backup/nip11.after.json"
"$python" "$collector" "$endpoint_wss" "$memphis" >"$backup/memphis.after.json"
"$python" "$collector" "$endpoint_wss" "$london" >"$backup/london.after.json"
cmp "$backup/memphis.before.json" "$backup/memphis.after.json"
cmp "$backup/london.before.json" "$backup/london.after.json"
docker inspect "$container" >"$backup/container.after.json"
(cd "$backup" && sha256sum ./* >SHA256SUMS)
completed=1
trap - EXIT HUP INT TERM
echo "Secondary production-neutral directory metadata accepted on 0.8.83. Backup: $backup"
echo 'Both owner-signed roots remained byte-identical across container recreation.'
