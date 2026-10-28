#!/bin/sh
# Let Amber retain a bounded set of exact-recipient NIP-46 subscriptions.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum --quiet -c REMOTE-SIGNER-RECOVERY-0.8.64-SHA256SUMS

target=/opt/bitcoinwalk-remote-signer/bitcoinwalk-remote-signer
old_sha=a6690129a311e632867090eb9e984913bab311a7fe3337ac47fae1946ec224bc
test "$(sha256sum "$target" | cut -d ' ' -f 1)" = "$old_sha" || {
	echo 'Installed remote signer is not the accepted 0.8.63 binary.' >&2
	exit 1
}

backup=$(mktemp -d /var/backups/bitcoinwalk-remote-signer-recovery.XXXXXX)
chmod 0700 "$backup"
cp -p "$target" "$backup/bitcoinwalk-remote-signer-0.8.63"

rollback() {
	code=$?
	trap - EXIT
	if [ "$code" -ne 0 ]; then
		install -o root -g root -m 0755 "$backup/bitcoinwalk-remote-signer-0.8.63" "$target"
		systemctl restart bitcoinwalk-remote-signer.service || true
		echo "Remote signer subscription recovery upgrade failed; 0.8.63 restored. Backup: $backup" >&2
	fi
	exit "$code"
}
trap rollback EXIT

install -o root -g root -m 0755 bitcoinwalk-relay-remote-signer-0.8.64 "$target"
systemctl restart bitcoinwalk-remote-signer.service

ready=false
for attempt in $(seq 1 20); do
	if curl --fail --silent --max-time 2 -H 'Accept: application/nostr+json' http://127.0.0.1:3344/ | grep -q 'bitcoinwalk-remote-signer-0.8.64'; then
		ready=true
		break
	fi
	sleep 1
done
test "$ready" = true
RELAY_SIGNER_ACCEPTANCE_URL=wss://remote.bitcoinwalk.org/ "$target" | tee "$backup/acceptance.log"
test ! -e /var/lib/bitcoinwalk-remote-signer

trap - EXIT
echo "Bounded NIP-46 subscription recovery 0.8.64 accepted. Backup: $backup"
