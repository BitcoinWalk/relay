#!/bin/sh
# Add a content-free, bearer-authenticated replication status endpoint to the
# source relay and expose it only through a signed super-admin staging action.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.22
archive=app-staging-0.3.60.tar.gz
manifest=REPLICATION-ALERTS-0.8.22-SHA256SUMS
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
memphis_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
firstwalk_target=/opt/bitcoinwalk-replica-firstwalk/bitcoinwalk-relay
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
memphis_db=/var/lib/bitcoinwalk-replica-rehearsal/events.db
firstwalk_db=/var/lib/bitcoinwalk-replica-firstwalk/events.db
source_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/30-replication-source.conf
app_unit=/etc/systemd/system/bitcoinwalk-app-staging.service
token=/etc/bitcoinwalk-replication/replica-status-token
previous_release=/opt/bitcoinwalk-app-staging/releases/0.3.59
release=/opt/bitcoinwalk-app-staging/releases/0.3.60
accepted_binary=cd44c8a69fb02f2657e8b61e7240bc10fc83d399030cd58181dff509171a010a
accepted_dropin=f950a0bad2c5fb4eaab83d486bb8488e6fa3a2dcb840d3836bc7a9bb72e02d44
accepted_app_unit=2d92a13c936692240ff0de5ea50d0489335757d339f61bdd890e80872873d019
accepted_registry=1241d159014a29498b24fc46bb279931aa37347413e6740cf04a323d62bf3604
accepted_caddy=4c994fa4c1d603bb9eb22d9ee1c3720812887ac562a3a51c30b14477033eeb2d
accepted_memphis_config=a758554fbf460a90e7f4b179fa2589366fd220505604856b47b114233436cfce
accepted_firstwalk_config=a0dd26a209974a3a6279560469eab229046d471a27b4829a083dad613ff6dac3
nashville=586c0d1f-e861-4c8f-858c-ce3e2bfaf384
nashville_replica=wss://replica-firstwalk-staging.bitcoinwalk.org/

digest() { sha256sum "$1" | cut -d ' ' -f 1; }
audit_retry() {
 attempt=0
 until "$@"; do
  attempt=$((attempt+1)); test "$attempt" -lt 8 || return 1
  echo 'Public audit has not converged; retrying in 5 seconds.' >&2
  sleep 5
 done
}
wait_health() {
 for port in 3334 3341 3342; do
  attempt=0
  until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
   attempt=$((attempt+1)); test "$attempt" -lt 25 || return 1; sleep 1
  done
  curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "http://127.0.0.1:$port/" | grep -q 'bitcoinwalk-organizers-0.8.22'
 done
 attempt=0
 until curl --fail --silent --max-time 3 http://127.0.0.1:3338/api/healthz | grep -q '"release":"app-staging-0.3.60"'; do
  attempt=$((attempt+1)); test "$attempt" -lt 25 || return 1; sleep 1
 done
}

sha256sum -c "$manifest"
for target in "$source_target" "$memphis_target" "$firstwalk_target"; do test "$(digest "$target")" = "$accepted_binary"; done
test "$(digest "$source_dropin")" = "$accepted_dropin"
test "$(digest "$app_unit")" = "$accepted_app_unit"
test "$(digest /etc/bitcoinwalk-replication/registry.json)" = "$accepted_registry"
test "$(digest /etc/bitcoinwalk-replication/receiver.conf)" = "$accepted_memphis_config"
test "$(digest /etc/bitcoinwalk-replication/receiver-firstwalk.conf)" = "$accepted_firstwalk_config"
test "$(digest /etc/caddy/Caddyfile)" = "$accepted_caddy"
test ! -e "$token"; test ! -e "$release"
grep -qx "WorkingDirectory=$previous_release" "$app_unit"
curl --fail --silent --max-time 5 http://127.0.0.1:3338/api/healthz | grep -q '"release":"app-staging-0.3.59"'
for unit in bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk bitcoinwalk-app-staging caddy; do systemctl is-active --quiet "$unit"; done

backup=$(mktemp -d /var/backups/bitcoinwalk-replication-alerts.XXXXXX)
chmod 0700 "$backup"
changed=0
completed=0
rollback() {
 code=$?
 trap - EXIT
 if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
  systemctl stop bitcoinwalk-app-staging.service bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service 2>/dev/null || true
  install -o root -g root -m 0755 "$backup/source-binary-0.8.21" "$source_target"
  install -o root -g root -m 0755 "$backup/memphis-binary-0.8.21" "$memphis_target"
  install -o root -g root -m 0755 "$backup/firstwalk-binary-0.8.21" "$firstwalk_target"
  install -o root -g root -m 0644 "$backup/source-dropin.before" "$source_dropin"
  install -o root -g root -m 0644 "$backup/app-service.before" "$app_unit"
  rm -f "$token"
  systemctl daemon-reload
  systemctl start bitcoinwalk-replica-rehearsal.service >/dev/null 2>&1 || true
  systemctl start bitcoinwalk-replica-firstwalk.service >/dev/null 2>&1 || true
  systemctl start bitcoinwalk-relay.service >/dev/null 2>&1 || true
  systemctl start bitcoinwalk-app-staging.service >/dev/null 2>&1 || true
  echo "0.8.22/0.3.60 activation failed; accepted executables and units restored without overwriting relay state. Backup: $backup" >&2
 fi
 if [ "$completed" -ne 1 ]; then echo 'Replication Alerts activation did not complete.' >&2; fi
 exit "$code"
}
trap rollback EXIT

systemctl stop bitcoinwalk-app-staging.service bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service
cp -p "$source_target" "$backup/source-binary-0.8.21"
cp -p "$memphis_target" "$backup/memphis-binary-0.8.21"
cp -p "$firstwalk_target" "$backup/firstwalk-binary-0.8.21"
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$memphis_db" "$backup/memphis-events.db"
cp -p "$firstwalk_db" "$backup/firstwalk-events.db"
cp -p "$source_dropin" "$backup/source-dropin.before"
cp -p "$app_unit" "$backup/app-service.before"
cp -p /etc/bitcoinwalk-replication/registry.json "$backup/registry.json"
cp -p /etc/bitcoinwalk-replication/receiver.conf "$backup/receiver.conf"
cp -p /etc/bitcoinwalk-replication/receiver-firstwalk.conf "$backup/receiver-firstwalk.conf"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile"
(
 cd "$backup"
 sha256sum source-binary-0.8.21 memphis-binary-0.8.21 firstwalk-binary-0.8.21 source-events.db source-replication-journal.db memphis-events.db firstwalk-events.db source-dropin.before app-service.before registry.json receiver.conf receiver-firstwalk.conf Caddyfile >SHA256SUMS
)
echo "Consistent pre-activation backup created: $backup"

changed=1
install -d -o root -g root -m 0755 /etc/bitcoinwalk-replication
umask 077
openssl rand -hex 32 >"$token"
chown root:root "$token"; chmod 0600 "$token"
install -d -m 0755 "$release"
tar -xzf "$archive" -C "$release"
test -f "$release/server.js"; test -f "$release/.next/BUILD_ID"; test ! -e "$release/.next/cache"; test -f "$release/node_modules/sharp/dist/index.cjs"
sharp_alias=$(find "$release/.next/node_modules" -maxdepth 1 -type l -name 'sharp-*' -print -quit)
test -n "$sharp_alias"; test "$(readlink "$sharp_alias")" = '../../node_modules/sharp'
ln -s /var/cache/bitcoinwalk-app-staging "$release/.next/cache"

{
 cat "$source_dropin"
 printf '%s\n' 'Environment=RELAY_REPLICA_STATUS_TOKEN_FILE=%d/replica-status-token'
 printf '%s\n' 'LoadCredential=replica-status-token:/etc/bitcoinwalk-replication/replica-status-token'
} >"$backup/source-dropin.candidate"
sed -e "s@^WorkingDirectory=$previous_release\$@WorkingDirectory=$release@" \
 -e '/^LoadCredential=openai-api-key:/a LoadCredential=replica-status-token:/etc/bitcoinwalk-replication/replica-status-token' \
 -e '/^Environment=OPENAI_API_KEY_FILE=/a Environment=REPLICATION_STATUS_TOKEN_FILE=%d/replica-status-token' \
 "$app_unit" >"$backup/bitcoinwalk-app-staging.service"
grep -qx 'Environment=RELAY_REPLICA_STATUS_TOKEN_FILE=%d/replica-status-token' "$backup/source-dropin.candidate"
grep -qx "WorkingDirectory=$release" "$backup/bitcoinwalk-app-staging.service"
grep -qx 'Environment=REPLICATION_STATUS_TOKEN_FILE=%d/replica-status-token' "$backup/bitcoinwalk-app-staging.service"
systemd-analyze verify "$backup/bitcoinwalk-app-staging.service" >/dev/null

install -o root -g root -m 0755 "$artifact" "$source_target"
install -o root -g root -m 0755 "$artifact" "$memphis_target"
install -o root -g root -m 0755 "$artifact" "$firstwalk_target"
install -o root -g root -m 0644 "$backup/source-dropin.candidate" "$source_dropin"
install -o root -g root -m 0644 "$backup/bitcoinwalk-app-staging.service" "$app_unit"
systemctl daemon-reload
systemctl start bitcoinwalk-replica-rehearsal.service
systemctl start bitcoinwalk-replica-firstwalk.service
systemctl start bitcoinwalk-relay.service
systemctl start bitcoinwalk-app-staging.service
wait_health

test "$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 5 http://127.0.0.1:3334/replication/status)" = 401
status_file="$backup/status.json"
curl --fail --silent --show-error --max-time 5 -H "Authorization: Bearer $(tr -d '\n' <"$token")" http://127.0.0.1:3334/replication/status >"$status_file"
grep -q '"version":1' "$status_file"; grep -q '"cities"' "$status_file"
if grep -Eqi 'eventId|event_id|envelope|bundle|attempt' "$status_file"; then echo 'Status response leaked replication internals.' >&2; exit 1; fi
test "$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 10 https://relay-staging.bitcoinwalk.org/replication/status)" = 401
test "$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 5 -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:3338/api/replication/status)" = 403
audit_retry ./replica-audit -city "$nashville" -replica "$nashville_replica" -allow-empty >/dev/null
audit_retry ./replica-audit >/dev/null
for unit in bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk bitcoinwalk-app-staging caddy; do systemctl is-active --quiet "$unit"; done

rm -f "$status_file"
completed=1
trap - EXIT
echo "Replication Alerts transport accepted on relay 0.8.22 and app 0.3.60. Backup: $backup"
echo 'The status credential is owner-only, unauthenticated relay access is rejected, and the app endpoint requires a signed super-admin request.'
echo 'Nashville remained 0/0 and Memphis remained 7/7.'
