#!/bin/sh
# Give Docker a private writable runtime configuration directory while keeping
# the worker's home directory and the rest of the host read-only.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on the paid-relay host.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

sha256sum -c CITY-RELAY-WORKER-RESUME-0.8.95-SHA256SUMS
test -f /etc/bitcoinwalk-city-relay-provisioner/token
test "$(stat -c %a /etc/bitcoinwalk-city-relay-provisioner/token)" = 600
test "$(getent ahostsv4 bitcoinwalk.org | awk 'NR==1{print $1}')" = 127.0.1.1
curl --fail --silent --show-error --max-time 15 --resolve bitcoinwalk.org:443:213.232.235.138 https://bitcoinwalk.org/api/healthz | grep -Fq '"release":"app-production-0.3.297"'

backup=$(mktemp -d /var/backups/bitcoinwalk-city-relay-worker-docker-config.XXXXXX)
chmod 0700 "$backup"
systemctl cat bitcoinwalk-city-relay-provisioner.service >"$backup/service.before"
install -d -o root -g root -m 0700 /opt/bitcoinwalk-city-relays /var/lib/bitcoinwalk-city-relays
install -o root -g root -m 0555 deploy/run-paid-city-relay-worker-0.8.95.sh /opt/bitcoinwalk-city-relay-provisioner/deploy/run-paid-city-relay-worker-0.8.95.sh
install -o root -g root -m 0644 deploy/bitcoinwalk-city-relay-provisioner-0.8.95.service /etc/systemd/system/bitcoinwalk-city-relay-provisioner.service
systemctl daemon-reload
systemctl reset-failed bitcoinwalk-city-relay-provisioner.service || true
systemctl cat bitcoinwalk-city-relay-provisioner.service >"$backup/service.after"
(cd "$backup" && sha256sum service.before service.after >SHA256SUMS)
echo "Paid-city relay worker private Docker configuration accepted on 0.8.95. Backup: $backup"
echo 'The worker remains stopped until the exact failed task is explicitly requeued.'
