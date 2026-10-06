#!/bin/sh
# Install the isolated, storage-free production NIP-46 transport.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum --quiet -c REMOTE-SIGNER-0.8.61-SHA256SUMS

host=remote.bitcoinwalk.org
expected_ip=213.232.235.138
listen=127.0.0.1:3344
target_dir=/opt/bitcoinwalk-remote-signer
target_binary=$target_dir/bitcoinwalk-remote-signer
unit=/etc/systemd/system/bitcoinwalk-remote-signer.service
caddy=/etc/caddy/Caddyfile
caddy_sha=328414cfa1902123f5273d3e0d2f4dd4b34bb53f72267dc931c1f3341d3a7c1b

test "$(sha256sum "$caddy" | cut -d ' ' -f 1)" = "$caddy_sha" || {
	echo 'Caddy configuration changed; stop for review.' >&2
	exit 1
}
getent ahostsv4 "$host" | awk '{print $1}' | sort -u | grep -qx "$expected_ip" || {
	echo "$host must resolve to $expected_ip before TLS activation." >&2
	exit 1
}
test ! -e "$target_dir"
test ! -e "$unit"
if ss -ltn '( sport = :3344 )' | grep -q LISTEN; then
	echo "$listen is already in use; nothing installed." >&2
	exit 1
fi

backup=$(mktemp -d /var/backups/bitcoinwalk-remote-signer.XXXXXX)
chmod 0700 "$backup"
cp -p "$caddy" "$backup/Caddyfile"
cp "$caddy" "$backup/Caddyfile.candidate"
cat deploy/Caddyfile.remote-signer >>"$backup/Caddyfile.candidate"
caddy validate --config "$backup/Caddyfile.candidate" --adapter caddyfile

rollback() {
	code=$?
	trap - EXIT
	if [ "$code" -ne 0 ]; then
		systemctl disable --now bitcoinwalk-remote-signer.service >/dev/null 2>&1 || true
		if [ -e "$backup/Caddyfile" ]; then
			install -o root -g root -m 0644 "$backup/Caddyfile" "$caddy"
			systemctl reload caddy >/dev/null 2>&1 || true
		fi
		rm -f "$unit"
		rm -rf "$target_dir"
		systemctl daemon-reload
		echo "Remote signer activation failed and was rolled back. Backup: $backup" >&2
	fi
	exit "$code"
}
trap rollback EXIT

install -d -o root -g root -m 0755 "$target_dir"
install -o root -g root -m 0755 bitcoinwalk-relay-remote-signer-0.8.61 "$target_binary"
install -o root -g root -m 0644 deploy/bitcoinwalk-remote-signer.service "$unit"
systemctl daemon-reload
systemctl enable --now bitcoinwalk-remote-signer.service

ready=false
for attempt in $(seq 1 20); do
	if curl --fail --silent --max-time 2 "http://$listen/healthz" | grep -q 'ephemeral-remote-signer'; then
		ready=true
		break
	fi
	sleep 1
done
test "$ready" = true
test "$(systemctl show bitcoinwalk-remote-signer.service --property=DynamicUser --value)" = yes
test ! -e /var/lib/bitcoinwalk-remote-signer
ss -ltn '( sport = :3344 )' | grep -q "$listen"

install -o root -g root -m 0644 "$backup/Caddyfile.candidate" "$caddy"
systemctl reload caddy

public_ready=false
for attempt in $(seq 1 30); do
	if curl --fail --silent --max-time 5 -H 'Accept: application/nostr+json' "https://$host/" >"$backup/nip11.json" 2>/dev/null; then
		public_ready=true
		break
	fi
	sleep 2
done
test "$public_ready" = true
grep -q 'BitcoinWalk remote signing transport' "$backup/nip11.json"
grep -q '46' "$backup/nip11.json"
RELAY_SIGNER_ACCEPTANCE_URL="wss://$host/" "$target_binary" | tee "$backup/acceptance.log"

trap - EXIT
echo "Dedicated BitcoinWalk remote signing transport 0.8.61 accepted. Backup: $backup"
echo "Endpoint: wss://$host/"
echo 'Only live kind 24133 delivery is enabled; events are not stored and no database was created.'
echo 'Existing application, event, chat, directory and replica services were not changed.'
