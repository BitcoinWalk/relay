#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this activation with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd)
cd "$root_dir"

manifest=INBOX-PRODUCTION-0.9.7-SHA256SUMS
audit="$root_dir/bitcoinwalk-inbox-audit-0.9.7"
snippet="$root_dir/deploy/inbox-production-0.9.7.caddy"
caddyfile=/etc/caddy/Caddyfile
host=inbox.bitcoinwalk.org
expected_ipv4=213.232.235.138

sha256sum -c "$manifest"
for command in caddy cp curl getent install mktemp sha256sum systemctl; do command -v "$command" >/dev/null; done
test -x "$audit"; test -f "$snippet"; test -f "$caddyfile"
test ! -L "$audit"; test ! -L "$snippet"; test ! -L "$caddyfile"
systemctl is-active --quiet bitcoinwalk-inbox-production.service
systemctl is-active --quiet bitcoinwalk-inbox-staging.service
curl -fsS --max-time 3 http://127.0.0.1:3349/healthz >/dev/null
"$audit" -relay wss://inbox-staging.bitcoinwalk.org/ -timeout 30s >/dev/null
getent ahostsv4 "$host" | awk -v expected="$expected_ipv4" '$1==expected { found=1 } END { exit !found }' || {
	echo "$host must have an explicit A record for $expected_ipv4 before public activation; the bitcoinwalk.org wildcard is not sufficient." >&2
	exit 1
}
! grep -Fq "$host {" "$caddyfile" || { echo "$host is already present in Caddy; stop for review." >&2; exit 1; }

backup=$(mktemp -d /var/backups/bitcoinwalk-inbox-production-https.XXXXXX)
chmod 0700 "$backup"
cp -p "$caddyfile" "$backup/Caddyfile.before"
candidate="$backup/Caddyfile.candidate"
cp -p "$caddyfile" "$candidate"
printf '\n' >>"$candidate"
cat "$snippet" >>"$candidate"
caddy validate --config "$candidate" --adapter caddyfile

changed=0
cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	if test "$status" -ne 0 && test "$changed" -eq 1; then
		install -o root -g root -m 0644 "$backup/Caddyfile.before" "$caddyfile"
		systemctl reload caddy || true
		echo "Inbox production HTTPS activation failed; the prior Caddy configuration was restored. Evidence: $backup" >&2
	fi
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

install -o root -g root -m 0644 "$candidate" "$caddyfile"
changed=1
systemctl reload caddy
for _ in $(seq 1 60); do
	if curl -fsS --max-time 5 "https://$host/healthz" >"$backup/public-health.json"; then break; fi
	sleep 1
done
curl -fsS --max-time 10 "https://$host/healthz" >"$backup/public-health.json"
curl -fsS --max-time 10 -H 'Accept: application/nostr+json' "https://$host/" >"$backup/public-nip11.json"
python3 - "$backup/public-nip11.json" <<'PY'
import json,sys
data=json.load(open(sys.argv[1],encoding="utf-8"))
assert data.get("name")=="BitcoinWalk private message inbox", data
assert data.get("version")=="bitcoinwalk-inbox-0.9.7", data
assert {17,42,59}.issubset(set(data.get("supported_nips",[]))), data
PY
"$audit" -relay "wss://$host/" -timeout 30s >"$backup/production-privacy.json"
systemctl is-active --quiet bitcoinwalk-inbox-production.service
systemctl is-active --quiet bitcoinwalk-inbox-staging.service
curl -fsS --max-time 5 https://inbox-staging.bitcoinwalk.org/healthz >/dev/null
sha256sum "$backup/Caddyfile.before" "$candidate" "$backup/public-health.json" "$backup/public-nip11.json" "$backup/production-privacy.json" >"$backup/SHA256SUMS"

trap - EXIT HUP INT TERM
echo "BitcoinWalk production inbox is public at wss://$host/. Evidence: $backup"
echo 'The production recipient-isolation audit passed. Existing event and NIP-29 relays and applications were not changed.'
