#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c LOGIN-TEST-SHA256SUMS
target=/srv/bitcoinwalk-chat-staging/armada/login-test.html
test -d /srv/bitcoinwalk-chat-staging/armada
test ! -e "$target" || { echo 'Test page already exists; stop for review.' >&2; exit 1; }
install -o root -g root -m 0644 login-test.html "$target"
echo 'Test page installed: https://chat-staging.bitcoinwalk.org/login-test.html'
echo 'No service, relay, Caddy configuration or existing web file changed.'
