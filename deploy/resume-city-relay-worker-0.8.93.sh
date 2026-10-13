#!/bin/sh
# Repair the first worker activation after systemd correctly rejected missing
# writable namespace roots. No task was claimed and no city state was changed.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on the paid-relay host.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

sha256sum --ignore-missing -c CITY-RELAY-PROVISIONING-0.8.92-SHA256SUMS
sha256sum -c CITY-RELAY-WORKER-RESUME-0.8.93-SHA256SUMS
test -x /opt/bitcoinwalk-city-relay-provisioner/deploy/run-paid-city-relay-worker-0.8.92.sh
test -f /etc/bitcoinwalk-city-relay-provisioner/token
test "$(stat -c %a /etc/bitcoinwalk-city-relay-provisioner/token)" = 600
test "$(systemctl is-active bitcoinwalk-city-relay-provisioner.timer)" = active

backup=$(mktemp -d /var/backups/bitcoinwalk-city-relay-worker-resume.XXXXXX)
chmod 0700 "$backup"
systemctl cat bitcoinwalk-city-relay-provisioner.service >"$backup/service.before"
install -d -o root -g root -m 0700 /opt/bitcoinwalk-city-relays /var/lib/bitcoinwalk-city-relays
install -o root -g root -m 0644 deploy/bitcoinwalk-city-relay-provisioner-0.8.93.service /etc/systemd/system/bitcoinwalk-city-relay-provisioner.service
systemctl daemon-reload
systemctl reset-failed bitcoinwalk-city-relay-provisioner.service
systemctl start bitcoinwalk-city-relay-provisioner.service
systemctl is-active --quiet bitcoinwalk-city-relay-provisioner.timer
systemctl cat bitcoinwalk-city-relay-provisioner.service >"$backup/service.after"
stat -c '%U:%G %a %n' /opt/bitcoinwalk-city-relays /var/lib/bitcoinwalk-city-relays >"$backup/writable-roots"
(cd "$backup" && sha256sum service.before service.after writable-roots >SHA256SUMS)
echo "Paid-city relay worker namespace repair accepted on 0.8.93. Backup: $backup"
echo 'The first 0.8.92 attempt claimed no task; the constrained worker is active through its timer.'
