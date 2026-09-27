#!/bin/sh
# Install the isolated directory transport on loopback only. Public TLS and DNS
# are deliberately deferred to a separate confirmation-gated increment.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-TRANSPORT-0.8.35-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-staging-0.8.35
collector=./deploy/capture-city-directory-root-0.8.35.mjs
unit_source=./deploy/bitcoinwalk-directory-staging.service
anchors_source=./deploy/city-directory-anchors-0.8.34.json
bundle_source=./deploy/city-directory-memphis-root-0.8.34.json
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
root_event=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
service=bitcoinwalk-directory-staging.service
target_root=/opt/bitcoinwalk-directory-staging
target=$target_root/bitcoinwalk-relay
config_root=/etc/bitcoinwalk-directory
anchors=$config_root/anchors.json
bundle=$config_root/memphis-bundle.json
unit=/etc/systemd/system/$service
database=/var/lib/bitcoinwalk-directory-staging/events.db
accepted_anchors=/etc/bitcoinwalk-relay/city-directory/anchors.json
accepted_bundle=/var/lib/bitcoinwalk-relay/city-directory-mirrors/memphis-bundled.json

sha256sum -c "$manifest"
test -x "$artifact"; test -x "$collector"; test -x "$node"
cmp -s "$accepted_anchors" "$anchors_source" || { echo 'Accepted durable anchor differs from this package.' >&2; exit 1; }
cmp -s "$accepted_bundle" "$bundle_source" || { echo 'Accepted signed bundle differs from this package.' >&2; exit 1; }
for dependency in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy; do systemctl is-active --quiet "$dependency"; done

backup=$(mktemp -d /var/backups/bitcoinwalk-directory-staging.XXXXXX)
chmod 0700 "$backup"
was_active=0
systemctl is-active --quiet "$service" 2>/dev/null && was_active=1 || true
test "$was_active" -eq 0 || systemctl stop "$service"

backup_target() {
 source=$1; label=$2
 if test -e "$source"; then
  test -f "$source" && test ! -L "$source"
  cp -p "$source" "$backup/$label"
 else
  : >"$backup/$label.absent"
 fi
}
restore_target() {
 destination=$1; label=$2
 if test -f "$backup/$label.absent"; then rm -f "$destination"; else install -D -p "$backup/$label" "$destination"; fi
}

backup_target "$target" binary
backup_target "$unit" unit
backup_target "$anchors" anchors.json
backup_target "$bundle" memphis-bundle.json
if test -f "$database"; then
 cp -p "$database" "$backup/events.db"
else
 : >"$backup/events.db.absent"
fi
echo "Consistent pre-activation directory transport backup created: $backup"

changed=0
completed=0
rollback() {
 code=$?; trap - EXIT HUP INT TERM
 if test "$changed" -eq 1; then
  systemctl disable --now "$service" >/dev/null 2>&1 || true
  restore_target "$target" binary
  restore_target "$unit" unit
  restore_target "$anchors" anchors.json
  restore_target "$bundle" memphis-bundle.json
  if test -f "$backup/events.db.absent"; then rm -f "$database"; else install -D -p "$backup/events.db" "$database"; fi
 systemctl daemon-reload
 test "$was_active" -eq 0 || systemctl start "$service" >/dev/null 2>&1 || true
 elif test "$was_active" -eq 1; then
  systemctl start "$service" >/dev/null 2>&1 || true
 fi
 if test "$completed" -ne 1; then echo "Directory transport activation did not complete; prior files were restored. Backup: $backup" >&2; fi
 exit "$code"
}
trap rollback EXIT HUP INT TERM

if ss -H -lnt | awk '{print $4}' | grep -Eq ':3343$'; then
 echo 'Loopback port 3343 is occupied; nothing changed.' >&2
 exit 1
fi

changed=1
install -d -o root -g root -m 0755 "$target_root" "$config_root"
install -o root -g root -m 0755 "$artifact" "$target"
install -o root -g root -m 0644 "$anchors_source" "$anchors"
install -o root -g root -m 0644 "$bundle_source" "$bundle"
install -o root -g root -m 0644 "$unit_source" "$unit"
systemctl daemon-reload
systemctl enable --now "$service"

wait_health() {
 attempt=0
 until curl --fail --silent --max-time 2 http://127.0.0.1:3343/healthz >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 20 || return 1; sleep 1
 done
}
audit_readback() {
 output=$1; report=$2
 "$node" "$collector" ws://127.0.0.1:3343/ "$root_event" >"$output"
 chmod 0644 "$output"
 env -i PATH=/usr/bin:/bin \
  RELAY_CITY_DIRECTORY_AUDIT_ANCHORS="$anchors" \
  RELAY_CITY_DIRECTORY_AUDIT_BUNDLE="$bundle" \
  RELAY_CITY_DIRECTORY_AUDIT_MIRRORS="$output" \
  RELAY_CITY_DIRECTORY_AUDIT_CITY=be8514a4-9df0-4159-a517-71f65761cbbe "$artifact" >"$report"
 grep -q '"bundleVerified": true' "$report"
 grep -q '"attestationCount": 1' "$report"
}

wait_health
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3343/ | grep -q 'bitcoinwalk-directory-transport-0.8.35'
audit_readback "$backup/readback-before-restart.json" "$backup/audit-before-restart.json"
systemctl restart "$service"
wait_health
audit_readback "$backup/readback-after-restart.json" "$backup/audit-after-restart.json"
cmp "$backup/readback-before-restart.json" "$backup/readback-after-restart.json"
cmp "$backup/audit-before-restart.json" "$backup/audit-after-restart.json"
for dependency in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy "$service"; do systemctl is-active --quiet "$dependency"; done
(
 cd "$backup"
 sha256sum readback-before-restart.json readback-after-restart.json audit-before-restart.json audit-after-restart.json >SHA256SUMS
 test ! -f events.db || sha256sum events.db >>SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "Loopback-only BitcoinWalk directory transport 0.8.35 accepted. Backup: $backup"
echo "Root event: $root_event"
echo 'The exact signed root remained readable across restart; the predecessor-preserving database is isolated from every relay and replica database.'
echo 'No Caddy or DNS configuration was changed. Add staging DNS only before the separate public-TLS activation step.'
