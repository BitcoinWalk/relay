#!/bin/sh
# Install an isolated public-certificate BW-56 receiver and opt the staging
# organizer relay into delivery for exactly one operator-selected city.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test -n "${REPLICA_CITY_ID:-}" || { echo 'Set REPLICA_CITY_ID.' >&2; exit 1; }
test -n "${REPLICA_DESTINATION:-}" || { echo 'Set REPLICA_DESTINATION.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.14
manifest=REPLICA-REHEARSAL-SHA256SUMS
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
receiver_root=/opt/bitcoinwalk-replica-rehearsal
config_root=/etc/bitcoinwalk-replication
receiver_unit=/etc/systemd/system/bitcoinwalk-replica-rehearsal.service
source_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/30-replication-source.conf
source_db=/var/lib/bitcoinwalk-relay/events.db

sha256sum -c "$manifest"
test "$(sha256sum "$source_target" | cut -d ' ' -f 1)" = d5b52d17b717b891c60b34c682e9576878645719b53c375ec1898c6f862bb79f || { echo 'Staging source binary changed; stop for review.' >&2; exit 1; }
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = 465a63aa0d2834e1c975ed40c9dcb32e59e3cbe3ae9242ff990cbbb024fd22e4 || { echo 'Caddy configuration changed; stop for review.' >&2; exit 1; }
test "$(sha256sum /etc/systemd/system/bitcoinwalk-relay.service | cut -d ' ' -f 1)" = 1c25b14231f91f6182c8f15de4670f57b8f7ea16e0f9e5c8e77e83027e3a9e8f || { echo 'Staging relay unit changed; stop for review.' >&2; exit 1; }
test "$(sha256sum /etc/systemd/system/bitcoinwalk-relay.service.d/20-organizers.conf | cut -d ' ' -f 1)" = a06c22b4b7ad5056e7d9218ff3c07c7a1f55a9669f44407b1b0108c4914da06f || { echo 'Organizer drop-in changed; stop for review.' >&2; exit 1; }

printf '%s\n' "$REPLICA_CITY_ID" | grep -Eq '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' || { echo 'REPLICA_CITY_ID must be a lowercase UUID.' >&2; exit 1; }
case "$REPLICA_DESTINATION" in
 wss://*.bitcoinwalk.org/) ;;
 *) echo 'REPLICA_DESTINATION must be an exact root wss://*.bitcoinwalk.org/ URL.' >&2; exit 1 ;;
esac
replica_host=${REPLICA_DESTINATION#wss://}
replica_host=${replica_host%/}
printf '%s\n' "$replica_host" | grep -Eq '^[a-z0-9]([a-z0-9.-]*[a-z0-9])?\.bitcoinwalk\.org$' || { echo 'Unsafe replica hostname.' >&2; exit 1; }
case "$replica_host" in *..*) echo 'Unsafe replica hostname.' >&2; exit 1 ;; esac
test "$(getent ahostsv4 "$replica_host" | awk 'NR==1 {print $1}')" = 213.232.235.138 || { echo 'Replica hostname does not explicitly resolve to the staging VPS.' >&2; exit 1; }

for target in "$receiver_root" "$config_root" "$receiver_unit" "$source_dropin" /var/lib/bitcoinwalk-replica-rehearsal /var/lib/private/bitcoinwalk-replica-rehearsal; do
 if [ -e "$target" ] || [ -L "$target" ]; then
  echo "Refusing existing target: $target" >&2
  exit 1
 fi
done
if ss -H -lnt | awk '{print $4}' | grep -Eq ':3341$'; then
 echo 'Loopback port 3341 is occupied; nothing changed.' >&2
 exit 1
fi
for unit in bitcoinwalk-relay caddy; do systemctl is-active --quiet "$unit"; done
curl --fail --silent --show-error --max-time 3 http://127.0.0.1:3334/healthz >/dev/null
if grep -Fq "$replica_host {" /etc/caddy/Caddyfile; then
 echo 'Replica hostname already exists in Caddy; nothing changed.' >&2
 exit 1
fi

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-rehearsal.XXXXXX)
chmod 0700 "$backup"
cp -p "$source_target" "$backup/bitcoinwalk-relay"
cp -p "$source_db" "$backup/events.db"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile"
cp -p /etc/systemd/system/bitcoinwalk-relay.service "$backup/bitcoinwalk-relay.service"
cp -p /etc/systemd/system/bitcoinwalk-relay.service.d/20-organizers.conf "$backup/20-organizers.conf"

sed "s/REPLICA_HOST/$replica_host/g" deploy/Caddyfile.replica-rehearsal >"$backup/Caddyfile.replica-rehearsal"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile.candidate"
printf '\n' >>"$backup/Caddyfile.candidate"
cat "$backup/Caddyfile.replica-rehearsal" >>"$backup/Caddyfile.candidate"
caddy validate --config "$backup/Caddyfile.candidate" --adapter caddyfile

changed=0
rollback() {
 code=$?
 trap - EXIT
 if [ "$code" -ne 0 ] && [ "$changed" -eq 1 ]; then
  systemctl disable --now bitcoinwalk-replica-rehearsal.service 2>/dev/null || true
  systemctl stop bitcoinwalk-relay.service 2>/dev/null || true
  install -o root -g root -m 0755 "$backup/bitcoinwalk-relay" "$source_target"
  rm -f "$source_dropin"
  install -o root -g root -m 0644 "$backup/Caddyfile" /etc/caddy/Caddyfile
  systemctl daemon-reload
  systemctl start bitcoinwalk-relay.service || true
  systemctl reload caddy.service || true
  echo "Activation failed; source binary and Caddy restored. New receiver files retained for diagnosis. Backup: $backup" >&2
 fi
 exit "$code"
}
trap rollback EXIT

install -d -o root -g root -m 0755 "$receiver_root"
# Registry and receiver.conf contain public scope only and must be traversable
# by the source DynamicUser. The secret itself remains root-owned mode 0600 and
# reaches the service exclusively through systemd LoadCredential.
install -d -o root -g root -m 0755 "$config_root"
install -o root -g root -m 0755 "$artifact" "$receiver_root/bitcoinwalk-relay"
service_pubkey=$(RELAY_REPLICA_KEY_INIT="$config_root/replica-delivery-key" "$receiver_root/bitcoinwalk-relay")
printf '%s\n' "$service_pubkey" | grep -Eq '^[0-9a-f]{64}$' || { echo 'Replica key initialization did not return a public key.' >&2; exit 1; }
install -m 0600 "$config_root/replica-delivery-key" "$backup/replica-delivery-key"
printf '%s\n' '{"version":1,"cities":[{"cityId":"'"$REPLICA_CITY_ID"'","destination":"'"$REPLICA_DESTINATION"'"}]}' >"$config_root/registry.json"
chmod 0644 "$config_root/registry.json"
{
 printf 'RELAY_REPLICA_RECEIVER_CITY=%s\n' "$REPLICA_CITY_ID"
 printf 'RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY=%s\n' "$service_pubkey"
 printf 'RELAY_REPLICA_RECEIVER_DESTINATION=%s\n' "$REPLICA_DESTINATION"
} >"$config_root/receiver.conf"
chmod 0644 "$config_root/receiver.conf"
install -o root -g root -m 0644 deploy/bitcoinwalk-replica-rehearsal.service "$receiver_unit"
install -o root -g root -m 0644 deploy/30-replication-source.conf "$source_dropin"

systemctl stop bitcoinwalk-relay.service
changed=1
install -o root -g root -m 0755 "$artifact" "$source_target"
install -o root -g root -m 0644 "$backup/Caddyfile.candidate" /etc/caddy/Caddyfile
systemctl daemon-reload
systemctl enable --now bitcoinwalk-replica-rehearsal.service
systemctl start bitcoinwalk-relay.service
systemctl reload caddy.service

for port in 3334 3341; do
 attempt=0
 until curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; do
  attempt=$((attempt+1)); test "$attempt" -lt 15 || exit 1; sleep 1
 done
done
curl --fail --silent --show-error --max-time 10 "https://$replica_host/healthz" >/dev/null
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.14'
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3341/ | grep -q 'bitcoinwalk-organizers-0.8.14'
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service

trap - EXIT
echo "Replication rehearsal services activated for one staging city. Backup: $backup"
echo "Service public key: $service_pubkey"
echo 'No production, chat, Guide, app or signing configuration was changed.'
echo 'Next: verify exact event IDs over both public WSS endpoints, then test restart/retry before retaining this configuration.'
