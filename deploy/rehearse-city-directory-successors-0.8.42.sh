#!/bin/sh
# Exercise the complete directory successor policy with generated in-memory
# identities, then prove both live Memphis transports remained byte-identical.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=CITY-DIRECTORY-SUCCESSOR-REHEARSAL-0.8.42-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-successors-0.8.42
collector=./deploy/capture-city-directory-root-0.8.35.mjs
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
anchor=/etc/bitcoinwalk-relay/city-directory/anchors.json
bundle=/var/lib/bitcoinwalk-relay/city-directory-mirrors/memphis-bundled.json
app_unit=/etc/systemd/system/bitcoinwalk-app-staging.service
root_event=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
primary=wss://directory-staging.bitcoinwalk.org/
secondary=wss://directory-2-staging.bitcoinwalk.org/

sha256sum -c "$manifest"
test -x "$artifact";test -x "$collector";test -x "$node"
for file in "$anchor" "$bundle" "$app_unit";do test -f "$file";test ! -L "$file";done
for unit in bitcoinwalk-app-staging bitcoinwalk-guide bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk bitcoinwalk-directory-staging caddy;do systemctl is-active --quiet "$unit";done
curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz|grep -q '"release":"app-staging-0.3.75"'

backup=$(mktemp -d /var/backups/bitcoinwalk-city-directory-successors.XXXXXX)
chmod 0700 "$backup"
cp -p "$anchor" "$backup/anchors.json"
cp -p "$bundle" "$backup/memphis-bundle.json"
cp -p "$app_unit" "$backup/app-service.unit"

capture(){
 relay=$1;output=$2;attempt=1
 while ! "$node" "$collector" "$relay" "$root_event" >"$output.tmp";do
  rm -f "$output.tmp"
  test "$attempt" -lt 4||return 1
  attempt=$((attempt+1));sleep 5
 done
 mv "$output.tmp" "$output"
}

capture "$primary" "$backup/primary-before.json"
capture "$secondary" "$backup/secondary-before.json"
cmp "$backup/primary-before.json" "$backup/secondary-before.json"

env -i PATH=/usr/bin:/bin RELAY_CITY_DIRECTORY_SUCCESSOR_REHEARSAL=isolated-successors-v1 "$artifact" >"$backup/rehearsal.json"
python3 - "$backup/rehearsal.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)

expected = ["owner-update", "operator-update", "rotate", "recover"]
transitions = report.get("transitions")
if not isinstance(transitions, list) or [item.get("name") for item in transitions] != expected:
    raise SystemExit("unexpected successor transition order")
if [item.get("sequence") for item in transitions] != [1, 2, 3, 4]:
    raise SystemExit("unexpected successor sequence")
for item in transitions:
    if len(item.get("eventId", "")) != 64 or len(item.get("signerPubkey", "")) != 64:
        raise SystemExit("invalid public rehearsal evidence")
for field in ["operatorEscalationRejected", "rotatedOwnerRejected", "conflictingSuccessorsRejected"]:
    if report.get(field) is not True:
        raise SystemExit(f"negative policy did not pass: {field}")
if report.get("finalSequence") != 4 or report.get("chainLength") != 5 or report.get("finalOperatorCount") != 0:
    raise SystemExit("unexpected final successor state")
if report.get("initialOwnerPubkey") == report.get("finalOwnerPubkey"):
    raise SystemExit("owner transition did not complete")
for forbidden in ["secret", "private", "nsec"]:
    if forbidden in json.dumps(report).lower():
        raise SystemExit("private material entered rehearsal report")
PY

capture "$primary" "$backup/primary-after.json"
capture "$secondary" "$backup/secondary-after.json"
cmp "$backup/primary-before.json" "$backup/primary-after.json"
cmp "$backup/secondary-before.json" "$backup/secondary-after.json"
cmp "$backup/primary-after.json" "$backup/secondary-after.json"
for unit in bitcoinwalk-app-staging bitcoinwalk-guide bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk bitcoinwalk-directory-staging caddy;do systemctl is-active --quiet "$unit";done
(
 cd "$backup"
 sha256sum anchors.json memphis-bundle.json app-service.unit primary-before.json primary-after.json secondary-before.json secondary-after.json rehearsal.json >SHA256SUMS
)

echo "Isolated directory successor rehearsal accepted. Backup: $backup"
echo 'Owner update, operator endpoint update, rotation and recovery passed in sequence.'
echo 'Operator escalation, rotated-owner writes and competing successors were rejected.'
echo 'The live Memphis root stayed identical on both public transports.'
echo 'No rehearsal event or executable was installed into a live path.'
