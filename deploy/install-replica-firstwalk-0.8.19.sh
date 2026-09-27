#!/bin/sh
# Add one disposable paid city to the fixed replication registry and activate a
# separate receiver. The existing Memphis receiver remains isolated and is
# audited before and after the additive migration.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.19
manifest=REPLICA-FIRSTWALK-0.8.19-SHA256SUMS
city_id=586c0d1f-e861-4c8f-858c-ce3e2bfaf384
destination=wss://replica-firstwalk-staging.bitcoinwalk.org/
replica_host=replica-firstwalk-staging.bitcoinwalk.org
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
memphis_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
receiver_root=/opt/bitcoinwalk-replica-firstwalk
config_root=/etc/bitcoinwalk-replication
registry=$config_root/registry.json
receiver_config=$config_root/receiver-firstwalk.conf
source_db=/var/lib/bitcoinwalk-relay/events.db
source_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
memphis_db=/var/lib/bitcoinwalk-replica-rehearsal/events.db
receiver_db=/var/lib/bitcoinwalk-replica-firstwalk/events.db
service_key=$config_root/replica-delivery-key
receiver_unit=/etc/systemd/system/bitcoinwalk-replica-firstwalk.service

accepted_hash=918a8b08c4c5500da2e7210230483ef282eb76637d449040cee91791ecfbd7dd
accepted_registry_hash=d662057b1566ffc804cffeb780115e6e543f4d90e5d27c5e437ad90535fb4c93
accepted_caddy_hash=e1540b17f14d1012fb94acdd50a71324a08dfe328f17d75ed32036b061133c5d
accepted_receiver_hash=a758554fbf460a90e7f4b179fa2589366fd220505604856b47b114233436cfce
accepted_dropin_hash=f950a0bad2c5fb4eaab83d486bb8488e6fa3a2dcb840d3836bc7a9bb72e02d44

run_public_audit() {
 attempt=0
 until "$@"; do
  attempt=$((attempt+1))
  test "$attempt" -lt 6 || return 1
  echo 'Public audit was rate-limited or temporarily unavailable; retrying in 5 seconds.' >&2
  sleep 5
 done
}

wait_public_health() {
 attempt=0
 until curl --fail --silent --max-time 5 "https://$replica_host/healthz" >/dev/null; do
  attempt=$((attempt+1))
  test "$attempt" -lt 30 || { echo 'Timed out waiting for the first-walk TLS endpoint.' >&2; return 1; }
  echo 'Waiting for Caddy to finish first-walk certificate activation.' >&2
  sleep 3
 done
}

sha256sum -c "$manifest"
test "$(sha256sum "$source_target" | cut -d ' ' -f 1)" = "$accepted_hash" || { echo 'Live source is not the accepted 0.8.18 binary.' >&2; exit 1; }
test "$(sha256sum "$memphis_target" | cut -d ' ' -f 1)" = "$accepted_hash" || { echo 'Live Memphis receiver is not the accepted 0.8.18 binary.' >&2; exit 1; }
test "$(sha256sum "$registry" | cut -d ' ' -f 1)" = "$accepted_registry_hash" || { echo 'Replication registry changed; stop for review.' >&2; exit 1; }
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = "$accepted_caddy_hash" || { echo 'Caddy configuration changed; stop for review.' >&2; exit 1; }
test "$(sha256sum "$config_root/receiver.conf" | cut -d ' ' -f 1)" = "$accepted_receiver_hash" || { echo 'Memphis receiver configuration changed; stop for review.' >&2; exit 1; }
test "$(sha256sum /etc/systemd/system/bitcoinwalk-relay.service.d/30-replication-source.conf | cut -d ' ' -f 1)" = "$accepted_dropin_hash" || { echo 'Source replication drop-in changed; stop for review.' >&2; exit 1; }
test -f "$source_db" && test -f "$source_journal" && test -f "$memphis_db"
test "$(stat -c '%a %U:%G' "$service_key")" = '600 root:root'
test "$(getent ahostsv4 "$replica_host" | awk 'NR==1 {print $1}')" = 213.232.235.138 || { echo 'First-walk hostname does not explicitly resolve to the staging VPS.' >&2; exit 1; }
service_pubkey=$(sed -n 's/^RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY=//p' "$config_root/receiver.conf")
printf '%s\n' "$service_pubkey" | grep -Eq '^[0-9a-f]{64}$' || { echo 'Invalid configured replication service public key.' >&2; exit 1; }

# A failed activation intentionally retains diagnostic receiver files. Permit a
# retry only when every retained public/configuration file is the exact package
# we are about to install and the receiver is stopped.
if [ -e "$receiver_root" ] || [ -L "$receiver_root" ]; then
 test -d "$receiver_root" && test "$(sha256sum "$receiver_root/bitcoinwalk-relay" | cut -d ' ' -f 1)" = "$(sha256sum "$artifact" | cut -d ' ' -f 1)" || { echo 'Retained receiver binary differs from this package.' >&2; exit 1; }
fi
if [ -e "$receiver_unit" ] || [ -L "$receiver_unit" ]; then
 test -f "$receiver_unit" && cmp -s "$receiver_unit" deploy/bitcoinwalk-replica-firstwalk.service || { echo 'Retained receiver unit differs from this package.' >&2; exit 1; }
fi
if [ -e "$receiver_config" ] || [ -L "$receiver_config" ]; then
 test -f "$receiver_config"
 test "$(wc -l <"$receiver_config")" -eq 3
 grep -Fqx "RELAY_REPLICA_RECEIVER_CITY=$city_id" "$receiver_config"
 grep -Fqx "RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY=$service_pubkey" "$receiver_config"
 grep -Fqx "RELAY_REPLICA_RECEIVER_DESTINATION=$destination" "$receiver_config"
fi
systemctl is-active --quiet bitcoinwalk-replica-firstwalk.service 2>/dev/null && { echo 'First-walk receiver unexpectedly active; stop for review.' >&2; exit 1; } || true
if ss -H -lnt | awk '{print $4}' | grep -Eq ':3342$'; then
 echo 'Loopback port 3342 is occupied; nothing changed.' >&2
 exit 1
fi
grep -Fq "$replica_host {" /etc/caddy/Caddyfile && { echo 'First-walk hostname already exists in Caddy; nothing changed.' >&2; exit 1; } || true
for unit in bitcoinwalk-relay bitcoinwalk-replica-rehearsal caddy; do systemctl is-active --quiet "$unit"; done

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-firstwalk.XXXXXX)
chmod 0700 "$backup"
candidate_registry=$backup/registry.candidate.json
printf '%s\n' '{"version":1,"cities":[{"cityId":"be8514a4-9df0-4159-a517-71f65761cbbe","destination":"wss://replica-staging.bitcoinwalk.org/"},{"cityId":"'$city_id'","destination":"'$destination'"}]}' >"$candidate_registry"
{
 printf 'RELAY_REPLICA_RECEIVER_CITY=%s\n' "$city_id"
 printf 'RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY=%s\n' "$service_pubkey"
 printf 'RELAY_REPLICA_RECEIVER_DESTINATION=%s\n' "$destination"
} >"$backup/receiver-firstwalk.conf"
sed "s/REPLICA_FIRSTWALK_HOST/$replica_host/g" deploy/Caddyfile.replica-firstwalk >"$backup/Caddyfile.replica-firstwalk"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile.candidate"
printf '\n' >>"$backup/Caddyfile.candidate"
cat "$backup/Caddyfile.replica-firstwalk" >>"$backup/Caddyfile.candidate"
caddy validate --config "$backup/Caddyfile.candidate" --adapter caddyfile

changed=0
completed=0
rollback() {
 code=$?
 trap - EXIT
 if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
  systemctl disable --now bitcoinwalk-replica-firstwalk.service 2>/dev/null || true
  systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service 2>/dev/null || true
  install -o root -g root -m 0755 "$backup/source-binary-0.8.18" "$source_target"
  install -o root -g root -m 0755 "$backup/memphis-binary-0.8.18" "$memphis_target"
  cp -p "$backup/source-replication-journal.db" "$source_journal"
  install -o root -g root -m 0644 "$backup/registry.json" "$registry"
  install -o root -g root -m 0644 "$backup/Caddyfile" /etc/caddy/Caddyfile
  systemctl daemon-reload
  systemctl start bitcoinwalk-replica-rehearsal.service >/dev/null 2>&1 || true
  systemctl start bitcoinwalk-relay.service >/dev/null 2>&1 || true
  systemctl reload caddy.service >/dev/null 2>&1 || true
  echo "0.8.19 activation failed; accepted Memphis configuration restored. New receiver files retained for diagnosis. Backup: $backup" >&2
 fi
 if [ "$completed" -ne 1 ]; then
  echo 'First-walk receiver rehearsal did not complete.' >&2
 fi
 exit "$code"
}
trap rollback EXIT

# Stop both writers before taking the recoverable, internally consistent copy.
systemctl stop bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service
cp -p "$source_target" "$backup/source-binary-0.8.18"
cp -p "$memphis_target" "$backup/memphis-binary-0.8.18"
cp -p "$source_db" "$backup/source-events.db"
cp -p "$source_journal" "$backup/source-replication-journal.db"
cp -p "$memphis_db" "$backup/memphis-events.db"
cp -p "$registry" "$backup/registry.json"
cp -p "$config_root/receiver.conf" "$backup/receiver.conf"
cp -p "$service_key" "$backup/replica-delivery-key"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile"
cp -p /etc/systemd/system/bitcoinwalk-relay.service.d/30-replication-source.conf "$backup/30-replication-source.conf"
cp -p /etc/systemd/system/bitcoinwalk-replica-rehearsal.service "$backup/bitcoinwalk-replica-rehearsal.service"
if [ -f "$receiver_db" ]; then
 cp -p "$receiver_db" "$backup/failed-firstwalk-events.db"
fi
(
 cd "$backup"
 sha256sum source-binary-0.8.18 memphis-binary-0.8.18 source-events.db source-replication-journal.db memphis-events.db registry.json registry.candidate.json receiver.conf receiver-firstwalk.conf replica-delivery-key Caddyfile Caddyfile.candidate 30-replication-source.conf bitcoinwalk-replica-rehearsal.service >SHA256SUMS
)
echo "Consistent pre-migration backup created: $backup"

# Never reuse a receiver database from an incomplete activation. Its exact
# contents remain recoverable in the new backup above.
if [ -f "$receiver_db" ]; then
 mv "$receiver_db" "$backup/failed-firstwalk-events.removed.db"
fi

changed=1
env \
 RELAY_REPLICA_JOURNAL="$source_journal" \
 RELAY_REPLICA_REGISTRY_ADD_CURRENT="$registry" \
 RELAY_REPLICA_REGISTRY_ADD_CANDIDATE="$candidate_registry" \
 RELAY_REPLICA_REGISTRY_ADD_CONFIRM=staging-registry-add-v1 \
 "./$artifact"

install -d -o root -g root -m 0755 "$receiver_root"
install -o root -g root -m 0755 "$artifact" "$source_target"
install -o root -g root -m 0755 "$artifact" "$memphis_target"
install -o root -g root -m 0755 "$artifact" "$receiver_root/bitcoinwalk-relay"
install -o root -g root -m 0644 "$candidate_registry" "$registry"
install -o root -g root -m 0644 "$backup/receiver-firstwalk.conf" "$receiver_config"
install -o root -g root -m 0644 deploy/bitcoinwalk-replica-firstwalk.service "$receiver_unit"
install -o root -g root -m 0644 "$backup/Caddyfile.candidate" /etc/caddy/Caddyfile
systemctl daemon-reload
systemctl enable --now bitcoinwalk-replica-firstwalk.service
systemctl start bitcoinwalk-replica-rehearsal.service
systemctl start bitcoinwalk-relay.service
systemctl reload caddy.service

for port in 3334 3341 3342; do
 attempt=0
 until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 15 || exit 1; sleep 1
 done
 curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "http://127.0.0.1:$port/" | grep -q 'bitcoinwalk-organizers-0.8.19'
done
wait_public_health
run_public_audit ./replica-audit >/dev/null
run_public_audit ./replica-audit -city "$city_id" -replica "$destination" -allow-empty
for unit in bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy; do systemctl is-active --quiet "$unit"; done

completed=1
trap - EXIT
echo "First-walk receiver activated with an empty public baseline. Backup: $backup"
echo "City: $city_id"
echo "Destination: $destination"
echo 'Memphis retained exact source/receiver equality. Do not approve Nashville until the hidden-event check is repeated.'
