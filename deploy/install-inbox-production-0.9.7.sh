#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd)
cd "$root_dir"

manifest=INBOX-PRODUCTION-0.9.7-SHA256SUMS
artifact="$root_dir/bitcoinwalk-inbox-production-0.9.7"
audit="$root_dir/bitcoinwalk-inbox-audit-0.9.7"
source_unit="$root_dir/deploy/bitcoinwalk-inbox-production-0.9.7.service"
target_dir=/opt/bitcoinwalk-inbox-production
target="$target_dir/bitcoinwalk-relay"
unit=/etc/systemd/system/bitcoinwalk-inbox-production.service
state=/var/lib/bitcoinwalk-inbox-production

sha256sum -c "$manifest"
for command in curl install journalctl mktemp python3 sha256sum ss systemctl systemd-analyze; do command -v "$command" >/dev/null; done
test -x "$artifact"; test -x "$audit"; test -f "$source_unit"
test ! -L "$artifact"; test ! -L "$audit"; test ! -L "$source_unit"
systemctl is-active --quiet bitcoinwalk-inbox-staging.service
curl -fsS --max-time 5 https://inbox-staging.bitcoinwalk.org/healthz >/dev/null

for path in "$target_dir" "$unit" "$state"; do
	test ! -e "$path" || { echo "Existing inbox production target requires review: $path" >&2; exit 1; }
done
! ss -ltnH '( sport = :3349 )' | grep -q . || { echo 'Loopback port 3349 is already in use.' >&2; exit 1; }

backup=$(mktemp -d /var/backups/bitcoinwalk-inbox-production.XXXXXX)
chmod 0700 "$backup"
"$audit" -relay wss://inbox-staging.bitcoinwalk.org/ -timeout 30s >"$backup/staging-privacy.json"
changed=0
cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	if test "$status" -ne 0 && test "$changed" -eq 1; then
		systemctl disable --now bitcoinwalk-inbox-production.service >/dev/null 2>&1 || true
		rm -f "$unit"
		rm -f "$target"
		rmdir "$target_dir" 2>/dev/null || true
		systemctl daemon-reload || true
		echo "Inbox production installation failed; new service files were removed. Database retained for diagnosis at $state. Evidence: $backup" >&2
	fi
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

install -d -o root -g root -m 0755 "$target_dir"
install -o root -g root -m 0755 "$artifact" "$target"
install -o root -g root -m 0644 "$source_unit" "$unit"
systemd-analyze verify "$unit" >/dev/null
changed=1
systemctl daemon-reload
systemctl enable --now bitcoinwalk-inbox-production.service

for _ in $(seq 1 30); do
	if curl -fsS --max-time 3 http://127.0.0.1:3349/healthz >"$backup/health.json"; then break; fi
	sleep 1
done
curl -fsS --max-time 3 http://127.0.0.1:3349/healthz >"$backup/health.json"
curl -fsS --max-time 3 -H 'Accept: application/nostr+json' http://127.0.0.1:3349/ >"$backup/nip11.json"
python3 - "$backup/nip11.json" <<'PY'
import json,sys
data=json.load(open(sys.argv[1],encoding="utf-8"))
assert data.get("name")=="BitcoinWalk private message inbox", data
assert data.get("version")=="bitcoinwalk-inbox-0.9.7", data
assert {1,11,17,40,42,59}.issubset(set(data.get("supported_nips",[]))), data
PY
systemctl is-active --quiet bitcoinwalk-inbox-production.service
systemctl is-active --quiet bitcoinwalk-inbox-staging.service
journalctl -u bitcoinwalk-inbox-production.service --since '2 minutes ago' --no-pager >"$backup/service.log"
sha256sum "$target" "$unit" "$backup/health.json" "$backup/nip11.json" "$backup/staging-privacy.json" >"$backup/installed.sha256"

trap - EXIT HUP INT TERM
echo "BitcoinWalk inbox production 0.9.7 is healthy on 127.0.0.1:3349. Evidence: $backup"
echo 'No DNS, Caddy, existing relay, chat service or app configuration was changed.'
