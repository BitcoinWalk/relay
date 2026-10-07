#!/bin/sh
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo on .138.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

service=bitcoinwalk-directory-staging.service
dropin_dir=/etc/systemd/system/bitcoinwalk-directory-staging.service.d
dropin="$dropin_dir/90-directory-production.conf"
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
collector=./deploy/capture-city-directory-root-0.8.35.mjs
memphis=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
london=d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2
endpoint=ws://127.0.0.1:3343/

sha256sum -c DIRECTORY-PRODUCTION-METADATA-0.8.83-SHA256SUMS
systemctl is-active --quiet "$service"
backup=$(mktemp -d /var/backups/bitcoinwalk-directory-production-metadata-primary.XXXXXX)
chmod 0700 "$backup"
systemctl cat "$service" >"$backup/service.before"
test ! -f "$dropin" || cp -p "$dropin" "$backup/dropin.before"
"$node" "$collector" "$endpoint" "$memphis" >"$backup/memphis.before.json"
"$node" "$collector" "$endpoint" "$london" >"$backup/london.before.json"

completed=0
restore() {
  code=$?
  trap - EXIT HUP INT TERM
  if test "$completed" -ne 1; then
    if test -f "$backup/dropin.before"; then
      install -o root -g root -m 0644 "$backup/dropin.before" "$dropin"
    else
      rm -f "$dropin"
    fi
    systemctl daemon-reload
    systemctl restart "$service" >/dev/null 2>&1 || true
    echo "Primary directory metadata activation did not complete; prior service configuration restored. Backup: $backup" >&2
  fi
  exit "$code"
}
trap restore EXIT HUP INT TERM

install -d -o root -g root -m 0755 "$dropin_dir"
install -o root -g root -m 0644 deploy/90-directory-production.conf "$dropin"
systemctl daemon-reload
systemctl restart "$service"

attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3343/healthz >/dev/null; do
  attempt=$((attempt + 1)); test "$attempt" -lt 30; sleep 1
done
curl -fsS --max-time 5 -H 'Accept: application/nostr+json' http://127.0.0.1:3343/ >"$backup/nip11.after.json"
grep -Fq '"name":"BitcoinWalk production directory relay"' "$backup/nip11.after.json"
"$node" "$collector" "$endpoint" "$memphis" >"$backup/memphis.after.json"
"$node" "$collector" "$endpoint" "$london" >"$backup/london.after.json"
cmp "$backup/memphis.before.json" "$backup/memphis.after.json"
cmp "$backup/london.before.json" "$backup/london.after.json"
systemctl is-active --quiet "$service"
(cd "$backup" && sha256sum ./* >SHA256SUMS)
completed=1
trap - EXIT HUP INT TERM
echo "Primary production-neutral directory metadata accepted on 0.8.83. Backup: $backup"
echo 'Both owner-signed roots remained byte-identical across restart.'
