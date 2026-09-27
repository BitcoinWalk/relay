#!/bin/sh
# Resume the exact retained state from the first BW-56 staging attempt, which
# rolled back because the source DynamicUser could not traverse the config dir.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."

artifact=bitcoinwalk-relay-replica-rehearsal-0.8.14
source_target=/opt/bitcoinwalk-relay/bitcoinwalk-relay
receiver_target=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
config_root=/etc/bitcoinwalk-replication
receiver_unit=/etc/systemd/system/bitcoinwalk-replica-rehearsal.service
source_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/30-replication-source.conf
source_db=/var/lib/bitcoinwalk-relay/events.db
city_id=be8514a4-9df0-4159-a517-71f65761cbbe
destination=wss://replica-staging.bitcoinwalk.org/
replica_host=replica-staging.bitcoinwalk.org

sha256sum -c REPLICA-REHEARSAL-SHA256SUMS
test "$(sha256sum "$source_target" | cut -d ' ' -f 1)" = d5b52d17b717b891c60b34c682e9576878645719b53c375ec1898c6f862bb79f || { echo 'Restored source binary changed; stop for review.' >&2; exit 1; }
test "$(sha256sum "$receiver_target" | cut -d ' ' -f 1)" = e4242c45089c5f72d8f6ae67013fb95cb3747297a75e516bbff5f9302d5faed1 || { echo 'Retained receiver binary changed; stop for review.' >&2; exit 1; }
test "$(sha256sum "$receiver_unit" | cut -d ' ' -f 1)" = e4a05636753866766d12af067b4d2af1840a5a20e9150a08a52baedde696d250 || { echo 'Retained receiver unit changed; stop for review.' >&2; exit 1; }
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = 465a63aa0d2834e1c975ed40c9dcb32e59e3cbe3ae9242ff990cbbb024fd22e4 || { echo 'Restored Caddy configuration changed; stop for review.' >&2; exit 1; }
test ! -e "$source_dropin" && test ! -L "$source_dropin" || { echo 'Source replication drop-in already exists; stop for review.' >&2; exit 1; }
test "$(stat -c %a "$config_root")" = 700 || { echo 'Retained config directory is not the expected rolled-back mode 0700.' >&2; exit 1; }
test "$(stat -c '%a %U:%G' "$config_root/registry.json")" = '644 root:root' || { echo 'Unexpected registry ownership or mode.' >&2; exit 1; }
test "$(stat -c '%a %U:%G' "$config_root/receiver.conf")" = '644 root:root' || { echo 'Unexpected receiver configuration ownership or mode.' >&2; exit 1; }
test "$(stat -c '%a %U:%G' "$config_root/replica-delivery-key")" = '600 root:root' || { echo 'Unexpected replication secret ownership or mode.' >&2; exit 1; }
grep -Fqx '{"version":1,"cities":[{"cityId":"be8514a4-9df0-4159-a517-71f65761cbbe","destination":"wss://replica-staging.bitcoinwalk.org/"}]}' "$config_root/registry.json"
grep -Fqx "RELAY_REPLICA_RECEIVER_CITY=$city_id" "$config_root/receiver.conf"
grep -Fqx "RELAY_REPLICA_RECEIVER_DESTINATION=$destination" "$config_root/receiver.conf"
service_pubkey=$(sed -n 's/^RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY=//p' "$config_root/receiver.conf")
printf '%s\n' "$service_pubkey" | grep -Eq '^[0-9a-f]{64}$' || { echo 'Invalid retained service public key.' >&2; exit 1; }
test "$(getent ahostsv4 "$replica_host" | awk 'NR==1 {print $1}')" = 213.232.235.138 || { echo 'Replica hostname does not explicitly resolve to the staging VPS.' >&2; exit 1; }
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet caddy.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service && { echo 'Receiver is unexpectedly active.' >&2; exit 1; } || true
systemctl is-enabled --quiet bitcoinwalk-replica-rehearsal.service && { echo 'Receiver is unexpectedly enabled.' >&2; exit 1; } || true
if ss -H -lnt | awk '{print $4}' | grep -Eq ':3341$'; then echo 'Loopback port 3341 is occupied.' >&2; exit 1; fi
grep -Fq "$replica_host {" /etc/caddy/Caddyfile && { echo 'Replica hostname already exists in Caddy.' >&2; exit 1; } || true
curl --fail --silent --show-error --max-time 3 http://127.0.0.1:3334/healthz >/dev/null

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-rehearsal-resume.XXXXXX)
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
  chmod 0700 "$config_root"
  install -o root -g root -m 0644 "$backup/Caddyfile" /etc/caddy/Caddyfile
  systemctl daemon-reload
  systemctl start bitcoinwalk-relay.service || true
  systemctl reload caddy.service || true
  echo "Resume failed; source binary, config-directory mode and Caddy restored. Backup: $backup" >&2
 fi
 exit "$code"
}
trap rollback EXIT

# Public scope files stay root-owned; only directory traversal changes. The
# root-owned 0600 secret remains inaccessible and is delivered by systemd.
chmod 0755 "$config_root"
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
attempt=0
until curl --fail --silent --show-error --max-time 5 "https://$replica_host/healthz" >/dev/null; do
 attempt=$((attempt+1)); test "$attempt" -lt 20 || exit 1; sleep 1
done
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.14'
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3341/ | grep -q 'bitcoinwalk-organizers-0.8.14'
systemctl is-active --quiet bitcoinwalk-relay.service
systemctl is-active --quiet bitcoinwalk-replica-rehearsal.service
systemctl is-active --quiet caddy.service

trap - EXIT
echo "Replication rehearsal resumed for Memphis. Backup: $backup"
echo "Service public key: $service_pubkey"
echo 'Next: verify exact Memphis event IDs over both public WSS endpoints and exercise restart/retry.'
