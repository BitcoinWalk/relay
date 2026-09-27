#!/bin/sh
set -eu
if [ "$(id -u)" -ne 0 ]; then
 echo 'Run this reviewed installer with sudo.' >&2
 exit 1
fi
cd "$(dirname "$0")/.."
sha256sum -c SHA256SUMS
if [ -e /etc/systemd/system/bitcoinwalk-relay.service ] || [ -e /opt/bitcoinwalk-relay ]; then
 echo 'An existing installation was found; refusing to overwrite it.' >&2
 exit 1
fi
install -d -m 0755 -o root -g root /opt/bitcoinwalk-relay
install -m 0755 -o root -g root bitcoinwalk-relay /opt/bitcoinwalk-relay/bitcoinwalk-relay
systemd-analyze verify deploy/bitcoinwalk-relay.service
install -m 0644 -o root -g root deploy/bitcoinwalk-relay.service /etc/systemd/system/bitcoinwalk-relay.service
systemctl daemon-reload
systemctl enable --now bitcoinwalk-relay.service
systemctl --no-pager status bitcoinwalk-relay.service
echo 'Relay installed on 127.0.0.1:3334 only. DNS and existing services were not changed.'
