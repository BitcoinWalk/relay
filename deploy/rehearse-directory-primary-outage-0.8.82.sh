#!/bin/sh
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-LONDON-OUTAGE-0.8.82-SHA256SUMS
collector=./deploy/capture-city-directory-root-0.8.35.mjs
expected_anchors=./deploy/city-directory-anchors-0.8.80.json
expected_bundle=./deploy/city-directory-memphis-london-bundle-0.8.80.json
anchors=/etc/bitcoinwalk-directory/anchors.json
bundle=/etc/bitcoinwalk-directory/memphis-bundle.json
service=bitcoinwalk-directory-staging.service
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
primary=wss://directory-staging.bitcoinwalk.org/
secondary=wss://directory-2-staging.bitcoinwalk.org/
memphis=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
london=d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2

sha256sum -c "$manifest"
cmp -s "$anchors" "$expected_anchors"
cmp -s "$bundle" "$expected_bundle"
test -x "$node"
systemctl is-active --quiet "$service"

backup=$(mktemp -d /var/backups/bitcoinwalk-directory-london-primary-outage.XXXXXX)
chmod 0700 "$backup"

capture_pair() {
  prefix=$1
  endpoint=$2
  "$node" "$collector" "$endpoint" "$memphis" >"$backup/memphis.$prefix.json"
  "$node" "$collector" "$endpoint" "$london" >"$backup/london.$prefix.json"
}

capture_pair primary.before "$primary"
capture_pair secondary.before "$secondary"
for city in memphis london; do
  cmp "$backup/$city.primary.before.json" "$backup/$city.secondary.before.json"
done

restored=0
completed=0
restore_primary() {
  code=$?
  trap - EXIT HUP INT TERM
  if test "$restored" -eq 0; then
    systemctl start "$service" >/dev/null 2>&1 || true
  fi
  if test "$completed" -ne 1; then
    echo "Primary directory outage rehearsal did not complete; the primary service was restored. Evidence: $backup" >&2
  fi
  exit "$code"
}
trap restore_primary EXIT HUP INT TERM

systemctl stop "$service"
if "$node" "$collector" "$primary" "$memphis" >"$backup/primary.unexpected-during-outage.json" 2>"$backup/primary.outage-error.log"; then
  echo 'Primary public directory remained readable after its service stopped.' >&2
  exit 1
fi
capture_pair secondary.during-primary-outage "$secondary"
for city in memphis london; do
  cmp "$backup/$city.secondary.before.json" "$backup/$city.secondary.during-primary-outage.json"
done

systemctl start "$service"
attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3343/healthz >/dev/null; do
  attempt=$((attempt + 1))
  test "$attempt" -lt 30 || exit 1
  sleep 1
done
restored=1

capture_pair primary.restored "$primary"
capture_pair secondary.after "$secondary"
for city in memphis london; do
  cmp "$backup/$city.primary.before.json" "$backup/$city.primary.restored.json"
  cmp "$backup/$city.primary.restored.json" "$backup/$city.secondary.after.json"
done
systemctl is-active --quiet "$service"
systemctl show "$service" --property=ActiveState,SubState,MainPID --no-pager >"$backup/primary-service.restored.txt"

(cd "$backup" && sha256sum ./*.json ./*.log ./*.txt >SHA256SUMS)
completed=1
trap - EXIT HUP INT TERM

echo "Primary-directory outage rehearsal accepted on 0.8.82. Evidence: $backup"
echo 'The secondary served the exact Memphis and London roots while the primary was stopped.'
echo 'The primary was restored and both public transports again returned identical signed state.'
echo 'No anchor, database, application, Guide, city relay, replica, DNS or Caddy state was changed.'
