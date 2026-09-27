#!/bin/sh
# Diagnostic only: separate loopback Caddy process, admin API disabled.
set -eu
scratch=$(mktemp -d /tmp/bitcoinwalk-route-check.XXXXXX)
awk 'BEGIN {print "{\n admin off\n auto_https off\n}"} /^chat-staging.bitcoinwalk.org \{/ {active=1; print "http://127.0.0.1:3346 {\n bind 127.0.0.1"; next} active {print}' /home/bitcoinwalk/bitcoinwalk-armada-cleanup-v2/deploy/Caddyfile.external-combined > "$scratch/Caddyfile"
caddy run --config "$scratch/Caddyfile" --adapter caddyfile > "$scratch/log" 2>&1 &
check_pid=$!
trap 'kill "$check_pid" 2>/dev/null || true; wait "$check_pid" 2>/dev/null || true' EXIT
attempt=0
until curl -fsS --max-time 2 http://127.0.0.1:3346/healthz >/dev/null; do
 attempt=$((attempt+1)); test "$attempt" -lt 5 || { cat "$scratch/log"; exit 1; }; sleep 1
done
kill -0 "$check_pid"
test "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:3346/signer)" = 410
test "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:3346/login-test.html)" = 302
curl -fsSL --max-redirs 2 http://127.0.0.1:3346/login-test.html | cmp -s - /home/bitcoinwalk/bitcoinwalk-armada-cleanup-v2/login-test.html
test "$(curl -sS -o /dev/null -w '%{redirect_url}' http://127.0.0.1:3346/s/chat-staging.bitcoinwalk.org/b7082b86a614153e)" = https://armada.buzz/s/chat-staging.bitcoinwalk.org/b7082b86a614153e
test "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:3346/assets/old.js)" = 404
echo 'PASS: isolated routing test — join-page redirect/content, channel redirect, signer 410, retired assets 404.'
