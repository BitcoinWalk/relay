#!/bin/sh
# Add the already accepted loopback directory transport to Caddy and prove
# exact signed-root equality over public TLS. The transport service and data are
# deliberately not restarted or modified.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-TLS-0.8.36-SHA256SUMS
fragment=./deploy/Caddyfile.directory-staging
collector=./deploy/capture-city-directory-root-0.8.35.mjs
audit=/opt/bitcoinwalk-directory-staging/bitcoinwalk-relay
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
anchors=/etc/bitcoinwalk-directory/anchors.json
bundle=/etc/bitcoinwalk-directory/memphis-bundle.json
host=directory-staging.bitcoinwalk.org
root_event=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
expected_ip=213.232.235.138
accepted_caddy=4c994fa4c1d603bb9eb22d9ee1c3720812887ac562a3a51c30b14477033eeb2d
accepted_transport=5588febe05886ad6d9a5ac4bb06f591ab53315f5825c771a411cbaba25e180e4

sha256sum -c "$manifest"
test -x "$collector"; test -x "$node"; test -x "$audit"
test "$(sha256sum /etc/caddy/Caddyfile | cut -d ' ' -f 1)" = "$accepted_caddy" || { echo 'Caddy configuration changed; stop for review.' >&2; exit 1; }
test "$(sha256sum "$audit" | cut -d ' ' -f 1)" = "$accepted_transport" || { echo 'Directory transport binary changed; stop for review.' >&2; exit 1; }
test "$(getent ahostsv4 "$host" | awk 'NR==1 {print $1}')" = "$expected_ip" || { echo 'Directory staging DNS does not resolve to the expected VPS.' >&2; exit 1; }
grep -Fq "$host {" /etc/caddy/Caddyfile && { echo 'Directory staging hostname is already configured; stop for review.' >&2; exit 1; } || true
for service in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk bitcoinwalk-directory-staging caddy; do systemctl is-active --quiet "$service"; done
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:3343/healthz >/dev/null

backup=$(mktemp -d /var/backups/bitcoinwalk-directory-tls.XXXXXX)
chmod 0700 "$backup"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile"
sed "s/DIRECTORY_STAGING_HOST/$host/g" "$fragment" >"$backup/Caddyfile.directory-staging"
cp -p /etc/caddy/Caddyfile "$backup/Caddyfile.candidate"
printf '\n' >>"$backup/Caddyfile.candidate"
cat "$backup/Caddyfile.directory-staging" >>"$backup/Caddyfile.candidate"
caddy validate --config "$backup/Caddyfile.candidate" --adapter caddyfile
echo "Consistent pre-activation Caddy backup created: $backup"

run_audit() {
 snapshot=$1; report=$2
 chmod 0644 "$snapshot"
 env -i PATH=/usr/bin:/bin \
  RELAY_CITY_DIRECTORY_AUDIT_ANCHORS="$anchors" \
  RELAY_CITY_DIRECTORY_AUDIT_BUNDLE="$bundle" \
  RELAY_CITY_DIRECTORY_AUDIT_MIRRORS="$snapshot" \
  RELAY_CITY_DIRECTORY_AUDIT_CITY=be8514a4-9df0-4159-a517-71f65761cbbe "$audit" >"$report"
 grep -q '"bundleVerified": true' "$report"
 grep -q '"attestationCount": 1' "$report"
}

"$node" "$collector" ws://127.0.0.1:3343/ "$root_event" >"$backup/readback-loopback.json"
run_audit "$backup/readback-loopback.json" "$backup/audit-loopback.json"

changed=0
completed=0
rollback() {
 code=$?; trap - EXIT HUP INT TERM
 if test "$changed" -eq 1; then
  install -o root -g root -m 0644 "$backup/Caddyfile" /etc/caddy/Caddyfile
  if ! systemctl reload caddy.service; then echo "URGENT: Caddy restoration reload failed. Backup: $backup" >&2; fi
 fi
 if test "$completed" -ne 1; then echo "Directory public-TLS activation did not complete; prior Caddyfile was restored. Backup: $backup" >&2; fi
 exit "$code"
}
trap rollback EXIT HUP INT TERM

changed=1
install -o root -g root -m 0644 "$backup/Caddyfile.candidate" /etc/caddy/Caddyfile
systemctl reload caddy.service

attempt=0
until curl --fail --silent --max-time 5 "https://$host/healthz" >/dev/null; do
 attempt=$((attempt+1)); test "$attempt" -lt 30 || exit 1
 echo 'Waiting for Caddy to finish directory-staging certificate activation.' >&2
 sleep 3
done
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' "https://$host/" | grep -q 'bitcoinwalk-directory-transport-0.8.35'
"$node" "$collector" "wss://$host/" "$root_event" >"$backup/readback-public.json"
run_audit "$backup/readback-public.json" "$backup/audit-public.json"
cmp "$backup/readback-loopback.json" "$backup/readback-public.json"
cmp "$backup/audit-loopback.json" "$backup/audit-public.json"
for service in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk bitcoinwalk-directory-staging caddy; do systemctl is-active --quiet "$service"; done
(
 cd "$backup"
 sha256sum Caddyfile Caddyfile.candidate Caddyfile.directory-staging readback-loopback.json readback-public.json audit-loopback.json audit-public.json >SHA256SUMS
)

completed=1
trap - EXIT HUP INT TERM
echo "BitcoinWalk directory staging public TLS accepted. Backup: $backup"
echo "Endpoint: wss://$host/"
echo "Root event: $root_event"
echo 'Public TLS and loopback returned the identical signed event and resolved state. The directory service and database were not restarted or changed.'
