#!/bin/sh
set -eu
test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c LOGIN-LINKS-SHA256SUMS
target=/srv/bitcoinwalk-chat-staging/armada/login-test.html
test -f "$target" && test ! -L "$target"
backup_dir=$(mktemp -d /var/backups/bitcoinwalk-login-links.XXXXXX)
chmod 0700 "$backup_dir"
cp -p "$target" "$backup_dir/login-test.html"
install -o root -g root -m 0644 login-test.html "$target"
echo "Both channel links updated. Previous page: $backup_dir/login-test.html"
echo 'Open https://chat-staging.bitcoinwalk.org/login-test.html and refresh.'
