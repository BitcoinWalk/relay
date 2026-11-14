#!/usr/bin/env bash
# Exact shared-relay upgrade only. Existing chat data and web policy are preserved.
set -euo pipefail
[[ $# == 1 && $1 == --apply && $(id -u) == 0 ]] || { echo 'Run with sudo bash SCRIPT --apply'; exit 2; }
exec 9>/run/lock/bitcoinwalk-chat-membership-production.lock
flock -n 9
package=$(cd -- "$(dirname -- "$0")" && pwd -P)
service=bitcoinwalk-chat.service
target=/opt/bitcoinwalk-chat/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-chat/chat.db
unit=/etc/systemd/system/bitcoinwalk-chat.service
config=/etc/bitcoinwalk-chat/public.conf
[[ -f $target && ! -L $target && -f $database && ! -L $database ]]
[[ $(sha256sum "$target" | cut -d ' ' -f1) == db5efeee38bb9199b0ed906ea82bc096c2c57dc1941df0342f1d43d10d4c4797 ]]
[[ $(sha256sum "$unit" | cut -d ' ' -f1) == 98d21d1fdccf9dbcbc955ab6f1909b240ab2bc41ca340d38f3ab0c7f970a82ef ]]
[[ $(sha256sum "$config" | cut -d ' ' -f1) == dd7a61a18b9cc6052b359e858116fabf6e34707ba92b45d39ccd5a30a9fcea86 ]]
[[ -z $(systemctl show "$service" -p DropInPaths --value) ]]
systemctl is-active --quiet "$service"
backup=$(mktemp -d /var/backups/bitcoinwalk-chat-membership-production.XXXXXX)
chmod 0700 "$backup"
for file in bitcoinwalk-relay SHA256SUMS; do
  [[ -f $package/$file && ! -L $package/$file ]]
  install -m 0600 "$package/$file" "$backup/$file"
done
[[ $(wc -l < "$backup/SHA256SUMS") == 1 ]]
grep -Eq '^[0-9a-f]{64}  bitcoinwalk-relay$' "$backup/SHA256SUMS"
(cd "$backup" && sha256sum --check --strict SHA256SUMS)
[[ $(sha256sum "$backup/bitcoinwalk-relay" | cut -d ' ' -f1) == e8bf5c1080f361a278a7e3e16b58c4ff434b9deb95210e916b4eb42bc857bca1 ]]
cp -a "$target" "$backup/previous-relay"
cp -a "$unit" "$backup/previous.service"
cp -a "$config" "$backup/public.conf"
echo "Production relay backup: $backup"
stopped=0
changed=0
recover() {
  result=$?
  if (( result != 0 && stopped == 1 )); then
    systemctl stop "$service" || true
    if (( changed == 1 )); then
      install -o root -g root -m 0755 "$backup/previous-relay" "$target.rollback"
      mv -T "$target.rollback" "$target"
    fi
    # Never restore an old database over messages accepted after the restart.
    systemctl start "$service" || true
    echo "Upgrade failed; previous binary restarted, database preserved. Backup: $backup" >&2
  fi
  exit "$result"
}
trap recover EXIT
stopped=1
systemctl stop "$service"
cp --reflink=auto "$database" "$backup/chat.db"
chmod 0600 "$backup/chat.db"
install -o root -g root -m 0755 "$backup/bitcoinwalk-relay" "$target.candidate"
mv -T "$target.candidate" "$target"
changed=1
systemctl start "$service"
curl -fsS --retry 12 --retry-delay 1 --retry-all-errors --max-time 2 http://127.0.0.1:3339/healthz > "$backup/health.json"
python3 -c 'import json,sys; x=json.load(open(sys.argv[1])); assert x.get("status")=="ok" and x.get("mode")=="persistent-chat-production"' "$backup/health.json"
status=$(curl -sS --max-time 5 -o "$backup/denial.txt" -w '%{http_code}' -H 'Content-Type: application/json' --data '{}' http://127.0.0.1:3339/api/chat/upload-authorization)
[[ $status == 401 ]]
systemctl is-active --quiet "$service"
stopped=0
echo 'PRODUCTION_MEMBERSHIP_RELAY_READY. Credentials, memberships and web enforcement unchanged.'
echo 'Next: expose the reviewed HTTPS authorization route; then test a signed member request.'
