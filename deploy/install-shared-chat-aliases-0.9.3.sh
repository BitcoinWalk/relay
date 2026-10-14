#!/usr/bin/env bash
set -euo pipefail

test "$(id -u)" -eq 0 || { echo "Run with sudo." >&2; exit 1; }
cd "$(dirname "$0")/.."
sha256sum -c SHARED-CHAT-ALIASES-0.9.3-SHA256SUMS

caddyfile=/etc/caddy/Caddyfile
service=caddy.service
chat_binary=/opt/bitcoinwalk-chat/bitcoinwalk-relay
test "$(sha256sum "$caddyfile" | cut -d ' ' -f 1)" = c0f1c6268af0f6cd315ca0617d7a6d065f0bf74f099b299bc59c738bcaa7f488 || { echo "Caddy configuration changed; stop for review." >&2; exit 1; }
test "$(sha256sum "$chat_binary" | cut -d ' ' -f 1)" = 08d214ff14600754eef76eb04ad9ad95092d9735ddc5d64307ae87b8d120c99a || { echo "Install and accept the shared NIP-29 relay upgrade first." >&2; exit 1; }
systemctl is-active --quiet "$service"
curl -fsS --max-time 5 http://127.0.0.1:3339/healthz >/dev/null

backup=$(mktemp -d /var/backups/bitcoinwalk-shared-chat-aliases.XXXXXX)
chmod 0700 "$backup"
cp -p "$caddyfile" "$backup/Caddyfile.before"
candidate="$backup/Caddyfile.candidate"

python3 - "$caddyfile" "$candidate" <<'PY'
from pathlib import Path
import sys

source=Path(sys.argv[1]).read_text(encoding="utf-8")
needle='''\t\thandle /join-chat {
\t\t\tredir https://armada.buzz/s/chat.bitcoinwalk.org/13bc3a423b4f2954 302
\t\t}
\t\thandle {
\t\t\trespond "BitcoinWalk community relay. Open the official Armada invitation from a BitcoinWalk event page." 404
\t\t}
'''
replacement='''\t\thandle /join-chat {
\t\t\tredir https://armada.buzz/s/chat.bitcoinwalk.org/13bc3a423b4f2954 302
\t\t}
\t\thandle /chat {
\t\t\theader Content-Type "text/html; charset=utf-8"
\t\t\trespond "<!doctype html><html lang='en'><meta name='viewport' content='width=device-width,initial-scale=1'><title>BitcoinWalk Global · Walkie Talkie</title><body><main><h1>BitcoinWalk Global</h1><p>Open the global channel in the Walkie Talkie app.</p><p><a href='walkietalkie://community?scope=global'>Open Walkie Talkie</a></p><p>This link is for Walkie Talkie. Armada and Concord use their existing BitcoinWalk community links.</p></main></body></html>" 200
\t\t}
\t\t@city_chat {
\t\t\tmethod GET HEAD
\t\t\tpath_regexp city ^/([a-z0-9]+(?:-[a-z0-9]+)*)/?$
\t\t}
\t\thandle @city_chat {
\t\t\theader Content-Type "text/html; charset=utf-8"
\t\t\trespond "<!doctype html><html lang='en'><meta name='viewport' content='width=device-width,initial-scale=1'><title>BitcoinWalk {re.city.1} · Walkie Talkie</title><body><main><h1>BitcoinWalk {re.city.1}</h1><p>Open this city channel in the Walkie Talkie app.</p><p><a href='walkietalkie://community?scope=city&city={re.city.1}'>Open Walkie Talkie</a></p><p>This link does not change the city's Armada or Concord relay.</p></main></body></html>" 200
\t\t}
\t\thandle {
\t\t\trespond "BitcoinWalk community relay. Open the official city or Global chat link from BitcoinWalk." 404
\t\t}
'''
if source.count(needle)!=1:
    raise SystemExit("The exact production chat route was not found once; nothing changed.")
Path(sys.argv[2]).write_text(source.replace(needle,replacement),encoding="utf-8")
PY

caddy validate --config "$candidate" --adapter caddyfile
accepted=0
restore() {
  if [ "$accepted" -ne 1 ]; then
    install -o root -g root -m 0644 "$backup/Caddyfile.before" "$caddyfile"
    systemctl reload "$service" || true
    echo "Shared chat alias activation failed; prior Caddy configuration restored. Backup: $backup" >&2
  fi
}
trap restore EXIT

install -o root -g root -m 0644 "$candidate" "$caddyfile"
systemctl reload "$service"
test "$(curl -sS --max-time 10 -o /dev/null -w '%{http_code}' https://chat.bitcoinwalk.org/chat)" = "200"
curl -fsS --max-time 10 https://chat.bitcoinwalk.org/chat | grep -F 'walkietalkie://community?scope=global' >/dev/null
test "$(curl -sS --max-time 10 -o /dev/null -w '%{http_code}' https://chat.bitcoinwalk.org/madeira)" = "200"
curl -fsS --max-time 10 https://chat.bitcoinwalk.org/madeira | grep -F 'walkietalkie://community?scope=city&city=madeira' >/dev/null
curl -fsS --max-time 10 -H 'Accept: application/nostr+json' https://chat.bitcoinwalk.org/ >"$backup/nip11.after.json"
python3 - "$backup/nip11.after.json" <<'PY'
import json,sys
data=json.load(open(sys.argv[1],encoding="utf-8"))
assert data.get("self")=="019f33d28e46ecf5c70ebd478c8dd6b6475cdd9eccdc7f1c533afd0eae8dc713", data
assert {1,11,29,42}.issubset(set(data.get("supported_nips",[]))), data
PY
curl -fsS --max-time 10 https://chat.bitcoinwalk.org/healthz >/dev/null
systemctl is-active --quiet "$service"
sha256sum "$backup/Caddyfile.before" "$candidate" "$backup/nip11.after.json" >"$backup/SHA256SUMS"

accepted=1
trap - EXIT
echo "Shared city chat aliases 0.9.3 accepted. Backup: $backup"
echo "Walkie Talkie links use https://chat.bitcoinwalk.org/<city>; Pro-city Armada and Concord relays remain unchanged."
