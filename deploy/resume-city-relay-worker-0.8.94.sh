#!/bin/sh
# Route only the worker's authenticated production API calls to .138 while
# retaining bitcoinwalk.org TLS/SNI. The .240 host keeps its legacy loopback
# mapping for every other service.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run as root on the paid-relay host.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

sha256sum -c CITY-RELAY-WORKER-RESUME-0.8.94-SHA256SUMS
test -f /etc/bitcoinwalk-city-relay-provisioner/token
test "$(stat -c %a /etc/bitcoinwalk-city-relay-provisioner/token)" = 600
test "$(getent ahostsv4 bitcoinwalk.org | awk 'NR==1{print $1}')" = 127.0.1.1
curl --fail --silent --show-error --max-time 15 --resolve bitcoinwalk.org:443:213.232.235.138 https://bitcoinwalk.org/api/healthz | grep -Fq '"release":"app-production-0.3.297"'

backup=$(mktemp -d /var/backups/bitcoinwalk-city-relay-worker-endpoint.XXXXXX)
chmod 0700 "$backup"
systemctl cat bitcoinwalk-city-relay-provisioner.service >"$backup/service.before"
install -d -o root -g root -m 0700 /opt/bitcoinwalk-city-relays /var/lib/bitcoinwalk-city-relays
install -o root -g root -m 0555 deploy/run-paid-city-relay-worker-0.8.94.sh /opt/bitcoinwalk-city-relay-provisioner/deploy/run-paid-city-relay-worker-0.8.94.sh
install -o root -g root -m 0644 deploy/bitcoinwalk-city-relay-provisioner-0.8.94.service /etc/systemd/system/bitcoinwalk-city-relay-provisioner.service
systemctl daemon-reload
systemctl reset-failed bitcoinwalk-city-relay-provisioner.service || true
systemctl start bitcoinwalk-city-relay-provisioner.service
systemctl enable --now bitcoinwalk-city-relay-provisioner.timer
systemctl is-active --quiet bitcoinwalk-city-relay-provisioner.timer
systemctl cat bitcoinwalk-city-relay-provisioner.service >"$backup/service.after"
(cd "$backup" && sha256sum service.before service.after >SHA256SUMS)
echo "Paid-city relay worker production endpoint accepted on 0.8.94. Backup: $backup"
echo 'Only this worker pins bitcoinwalk.org to .138; the host-wide legacy mapping remains unchanged.'
