#!/bin/sh
# One-time privileged bootstrap. Future exact paid-city jobs are claimed by the
# constrained oneshot worker; deployed relay and gateway containers are non-root.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on the paid-relay host.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /absolute/path/to/shared-token" >&2; exit 1; }
cd "$(dirname "$0")/.."
case "$1" in /*) token_source=$1;; *) echo 'Token path must be absolute.' >&2; exit 1;; esac
test "$(readlink -f "$token_source")" = "$token_source" && test -f "$token_source" && test ! -L "$token_source"
test "$(stat -c %a "$token_source")" = 600

manifest=CITY-RELAY-PROVISIONING-0.8.92-SHA256SUMS
target=/opt/bitcoinwalk-city-relay-provisioner
credential=/etc/bitcoinwalk-city-relay-provisioner/token
unit=/etc/systemd/system/bitcoinwalk-city-relay-provisioner.service
timer=/etc/systemd/system/bitcoinwalk-city-relay-provisioner.timer
sha256sum --ignore-missing -c "$manifest"
test -x bitcoinwalk-relay-city-0.8.92 && test -x replica-audit-city-0.8.92
test ! -e "$target" && test ! -e "$(dirname "$credential")" && test ! -e "$unit" && test ! -e "$timer"
backup=$(mktemp -d /var/backups/bitcoinwalk-city-relay-worker.XXXXXX);chmod 0700 "$backup"
install -d -o root -g root -m 0700 "$target" "$target/deploy" "$(dirname "$credential")"
install -o root -g root -m 0555 bitcoinwalk-relay-city-0.8.92 "$target/bitcoinwalk-relay-city-0.8.92"
install -o root -g root -m 0555 replica-audit-city-0.8.92 "$target/replica-audit-city-0.8.92"
install -o root -g root -m 0444 CITY-RELAY-PROVISIONING-0.8.92-SHA256SUMS "$target/CITY-RELAY-PROVISIONING-0.8.92-SHA256SUMS"
install -o root -g root -m 0444 deploy/Dockerfile.city-relay-0.8.92 "$target/deploy/Dockerfile.city-relay-0.8.92"
install -o root -g root -m 0555 deploy/run-paid-city-relay-worker-0.8.92.sh "$target/deploy/run-paid-city-relay-worker-0.8.92.sh"
install -o root -g root -m 0555 deploy/provision-paid-city-relay-0.8.92.sh "$target/deploy/provision-paid-city-relay-0.8.92.sh"
install -o root -g root -m 0600 "$token_source" "$credential"
install -o root -g root -m 0644 deploy/bitcoinwalk-city-relay-provisioner-0.8.92.service "$unit"
install -o root -g root -m 0644 deploy/bitcoinwalk-city-relay-provisioner-0.8.92.timer "$timer"
systemctl daemon-reload
systemctl enable --now bitcoinwalk-city-relay-provisioner.timer
systemctl start bitcoinwalk-city-relay-provisioner.service
systemctl is-active --quiet bitcoinwalk-city-relay-provisioner.timer
echo "Paid-city relay worker accepted on 0.8.92. Backup: $backup"
echo 'The web app can submit only exact paid approvals; every relay and gateway container runs non-root with no host-published port.'
