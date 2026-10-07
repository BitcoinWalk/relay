#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo on .138.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=DIRECTORY-ACTIVATION-0.8.84-SHA256SUMS
artifact=./bitcoinwalk-relay-directory-activation-0.8.84
target=/opt/bitcoinwalk-directory-staging/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-directory-staging/events.db
service=bitcoinwalk-directory-staging.service
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
collector=./deploy/capture-city-directory-root-0.8.35.mjs
endpoint=ws://127.0.0.1:3343/
memphis=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
london=d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2
predecessor=014c3c9529c3272f8a9cef2fc01abae647988e2a00b8324c1c08b6480697a721

sha256sum -c "$manifest"
test "$(sha256sum "$target" | cut -d ' ' -f 1)" = "$predecessor" || { echo 'Installed primary directory binary is not the accepted 0.8.79 predecessor.' >&2; exit 1; }
systemctl is-active --quiet "$service"
backup=$(mktemp -d /var/backups/bitcoinwalk-directory-activation-primary.XXXXXX);chmod 0700 "$backup"
cp -p "$target" "$backup/bitcoinwalk-relay.before"
systemctl cat "$service" >"$backup/service.before"
"$node" "$collector" "$endpoint" "$memphis" >"$backup/memphis.before.json"
"$node" "$collector" "$endpoint" "$london" >"$backup/london.before.json"

changed=0;completed=0
restore(){ code=$?;trap - EXIT HUP INT TERM;if test "$changed" -eq 1;then systemctl stop "$service" >/dev/null 2>&1||true;install -o root -g root -m 0755 "$backup/bitcoinwalk-relay.before" "$target";test ! -f "$backup/events.db"||cp -p "$backup/events.db" "$database";systemctl start "$service" >/dev/null 2>&1||true;fi;test "$completed" -eq 1||echo "Primary directory activation-policy upgrade did not complete; accepted service restored. Backup: $backup" >&2;exit "$code";}
trap restore EXIT HUP INT TERM
systemctl stop "$service";cp -p "$database" "$backup/events.db";sync "$backup/events.db";changed=1
install -o root -g root -m 0755 "$artifact" "$target";systemctl start "$service"
attempt=0;until curl -fsS --max-time 2 http://127.0.0.1:3343/healthz >/dev/null;do attempt=$((attempt+1));test "$attempt" -lt 30;sleep 1;done
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3343/ >"$backup/nip11.after.json"
grep -Fq 'bitcoinwalk-directory-transport-0.8.84' "$backup/nip11.after.json"
"$node" "$collector" "$endpoint" "$memphis" >"$backup/memphis.after.json"
"$node" "$collector" "$endpoint" "$london" >"$backup/london.after.json"
cmp "$backup/memphis.before.json" "$backup/memphis.after.json";cmp "$backup/london.before.json" "$backup/london.after.json"
systemctl restart "$service";attempt=0;until curl -fsS --max-time 2 http://127.0.0.1:3343/healthz >/dev/null;do attempt=$((attempt+1));test "$attempt" -lt 30;sleep 1;done
"$node" "$collector" "$endpoint" "$memphis" >"$backup/memphis.after-restart.json";"$node" "$collector" "$endpoint" "$london" >"$backup/london.after-restart.json"
cmp "$backup/memphis.after.json" "$backup/memphis.after-restart.json";cmp "$backup/london.after.json" "$backup/london.after-restart.json"
(cd "$backup" && sha256sum ./* >SHA256SUMS)
completed=1;trap - EXIT HUP INT TERM
echo "Primary directory operator-transport policy accepted on 0.8.84. Backup: $backup"
echo 'Owner signatures and pinned-chain validation remain mandatory; a current endpoint operator may authenticate transport only.'
echo 'Both owner-signed roots remained byte-identical across activation and restart.'
