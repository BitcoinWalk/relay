#!/bin/sh
# Upgrade the storage-free signer transport with metadata-free handshake diagnostics.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum --quiet -c REMOTE-SIGNER-DIAGNOSTICS-0.8.62-SHA256SUMS

target=/opt/bitcoinwalk-remote-signer/bitcoinwalk-remote-signer
old_sha=97f3a6983283f9dcf97be9d4b97b419b2e4b021121fba3a72c942924157c373f
test "$(sha256sum "$target" | cut -d ' ' -f 1)" = "$old_sha" || {
	echo 'Installed remote signer is not the accepted 0.8.61 binary.' >&2
	exit 1
}

backup=$(mktemp -d /var/backups/bitcoinwalk-remote-signer-diagnostics.XXXXXX)
chmod 0700 "$backup"
cp -p "$target" "$backup/bitcoinwalk-remote-signer-0.8.61"

rollback() {
	code=$?
	trap - EXIT
	if [ "$code" -ne 0 ]; then
		install -o root -g root -m 0755 "$backup/bitcoinwalk-remote-signer-0.8.61" "$target"
		systemctl restart bitcoinwalk-remote-signer.service || true
		echo "Diagnostic upgrade failed; 0.8.61 restored. Backup: $backup" >&2
	fi
	exit "$code"
}
trap rollback EXIT

install -o root -g root -m 0755 bitcoinwalk-relay-remote-signer-0.8.62 "$target"
systemctl restart bitcoinwalk-remote-signer.service

ready=false
for attempt in $(seq 1 20); do
	if curl --fail --silent --max-time 2 -H 'Accept: application/nostr+json' http://127.0.0.1:3344/ | grep -q 'bitcoinwalk-remote-signer-0.8.62'; then
		ready=true
		break
	fi
	sleep 1
done
test "$ready" = true
RELAY_SIGNER_ACCEPTANCE_URL=wss://remote.bitcoinwalk.org/ "$target" | tee "$backup/acceptance.log"
test ! -e /var/lib/bitcoinwalk-remote-signer

trap - EXIT
echo "Privacy-safe NIP-46 handshake diagnostics accepted on 0.8.62. Backup: $backup"
echo 'Logs record only connection and accept/reject outcomes; no pubkeys, event IDs, tags or encrypted content are logged.'
