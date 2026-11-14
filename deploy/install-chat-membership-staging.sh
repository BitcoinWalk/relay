#!/usr/bin/env bash
# Upgrade only the already-installed, stopped staging relay. No proxy/web edits.
set -euo pipefail
if [[ $# != 1 || $1 != --apply ]]; then
  echo "Usage: sudo bash $0 --apply" >&2
  exit 2
fi
[[ $(id -u) == 0 ]] || { echo 'Run with sudo.' >&2; exit 1; }
exec 9>/run/lock/bitcoinwalk-chat-membership-staging.lock
flock --exclusive --nonblock 9 || { echo 'Another staging upgrade is running.' >&2; exit 1; }
package=$(cd -- "$(dirname -- "$0")" && pwd -P)
service=bitcoinwalk-chat-staging.service
target=/opt/bitcoinwalk-chat-staging/bitcoinwalk-relay
config=/etc/bitcoinwalk-chat-staging/public.conf
database=/var/lib/bitcoinwalk-chat-staging/chat.db
prior=8a4a656573ea92f7af9cda3d3fdbfc58d750386bd2e12ad4751b536b8eda7dc4
fail() { echo "$* Nothing activated." >&2; exit 1; }
[[ $(uname -m) == x86_64 ]] || fail 'Expected x86_64 staging host.'
[[ -f $target && ! -L $target ]] || fail 'Unexpected relay binary target.'
[[ $(sha256sum "$target" | cut -d ' ' -f1) == "$prior" ]] || fail 'Staging relay changed; review before upgrading.'
[[ $(systemctl show "$service" -p ActiveState --value) == inactive ]] || fail 'Staging must be stopped before this installer runs.'
[[ -z $(systemctl show "$service" -p DropInPaths --value) ]] || fail 'Service overrides need review.'
[[ $(systemctl show "$service" -p FragmentPath --value) == /etc/systemd/system/bitcoinwalk-chat-staging.service ]] || fail 'Unexpected service unit.'
cmp -s "$package/staging.service" /etc/systemd/system/bitcoinwalk-chat-staging.service || fail 'Staging service definition changed.'
[[ -f $config && ! -L $config ]] || fail 'Missing public staging configuration.'
public_config=$(sed '/^[[:space:]]*#/d; /^[[:space:]]*$/d' "$config")
[[ $public_config == 'RELAY_CHAT_URL=wss://chat-staging.bitcoinwalk.org' ]] || fail 'Staging public configuration needs review.'
[[ -f $database && ! -L $database ]] || fail 'Existing staging database required; refusing to initialize empty state.'
[[ -f /etc/bitcoinwalk-chat-staging/relay-key ]] || fail 'Existing relay credential required.'
listeners=$(ss -H -lnt 'sport = :3335')
[[ -z $listeners ]] || fail 'Port 3335 is already in use.'

# Snapshot artifacts into root-only storage before checking/using their bytes.
backup=$(mktemp -d /var/backups/bitcoinwalk-chat-membership-staging.XXXXXX)
chmod 0700 "$backup"
for file in bitcoinwalk-relay staging.service SHA256SUMS; do
  [[ -f $package/$file && ! -L $package/$file ]] || fail "Missing regular package file: $file"
  install -m 0600 "$package/$file" "$backup/$file"
done
[[ $(wc -l < "$backup/SHA256SUMS") == 2 ]]
[[ $(grep -Ec '^[0-9a-f]{64}  (bitcoinwalk-relay|staging.service)$' "$backup/SHA256SUMS") == 2 ]]
[[ $(cut -c67- "$backup/SHA256SUMS" | sort -u | wc -l) == 2 ]]
(cd "$backup" && sha256sum --check --strict SHA256SUMS)
cmp -s "$backup/staging.service" /etc/systemd/system/bitcoinwalk-chat-staging.service || fail 'Unit snapshot changed.'
cp -a "$target" "$backup/previous-relay"
cp --reflink=auto "$database" "$backup/chat.db"
chmod 0600 "$backup/chat.db"
cp -a /etc/systemd/system/bitcoinwalk-chat-staging.service "$backup/previous.service"
echo "Staging-only backup: $backup"

changed=0
recover() {
  status=$?
  if (( status != 0 && changed == 1 )); then
    systemctl stop "$service" || true
    echo "Upgrade did not pass health checks. Staging stopped; backup: $backup" >&2
    echo 'Database is NOT automatically restored. Review before rollback.' >&2
  fi
  exit "$status"
}
trap recover EXIT
install -o root -g root -m 0755 "$backup/bitcoinwalk-relay" "$target.candidate"
mv -T "$target.candidate" "$target"
changed=1
systemctl start "$service"
ready=0
for attempt in {1..15}; do
  if curl --fail --silent --show-error --max-time 2 http://127.0.0.1:3335/healthz > "$backup/health.json"; then ready=1; break; fi
  sleep 1
done
[[ $ready == 1 ]]
python3 -c 'import json,sys; x=json.load(open(sys.argv[1])); assert x.get("status")=="ok" and x.get("mode")=="persistent-chat-staging"' "$backup/health.json"
status=$(curl --silent --show-error --max-time 5 -o "$backup/authorization-denial.txt" -w '%{http_code}' -H 'Content-Type: application/json' --data '{}' http://127.0.0.1:3335/api/chat/upload-authorization)
[[ $status == 401 ]]
systemctl is-active --quiet "$service"
echo 'STAGING_MEMBERSHIP_RELAY_READY: health passed; unsigned upload authorization denied.'
echo 'No Caddy, web enforcement, production service, credential, or membership changes made.'
echo 'Next: review the staging HTTPS proxy route, then enable staging web enforcement.'
