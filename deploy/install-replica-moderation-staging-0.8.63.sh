#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"

manifest=REPLICA-MODERATION-0.8.63-SHA256SUMS
binary="$root_dir/bitcoinwalk-relay-replica-moderation-0.8.63"
audit="$root_dir/replica-audit-shadow-0.8.44"
source_binary=/opt/bitcoinwalk-relay/bitcoinwalk-relay
receiver_binary=/opt/bitcoinwalk-replica-rehearsal/bitcoinwalk-relay
source_dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/90-bw08-version.conf
receiver_dropin=/etc/systemd/system/bitcoinwalk-replica-rehearsal.service.d/90-bw08-version.conf
city=be8514a4-9df0-4159-a517-71f65761cbbe
source=wss://relay-staging.bitcoinwalk.org/
receiver=wss://replica-staging.bitcoinwalk.org/

verify_report(){
 python3 - "$1" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if not report["readOnly"] or not report["exactEventIds"]:
    raise SystemExit("replica audit is not exact and read-only")
if report["source"]["occurrenceIds"] != report["replica"]["occurrenceIds"]:
    raise SystemExit("source and receiver occurrence sets differ")
for side in ("source", "replica"):
    if not report[side]["allSignaturesValid"] or report[side]["privateWrapperCount"] != 0:
        raise SystemExit("replica signature or privacy acceptance failed")
PY
}
audit_exact(){
 "$audit" -source "$source" -replica "$receiver" -city "$city" -allow-empty >"$1"
 verify_report "$1"
}
wait_service(){
 local unit=$1 url=$2
 local ready=false
 for attempt in $(seq 1 30);do
  if systemctl is-active --quiet "$unit"&&curl --fail --silent --max-time 3 "$url" >/dev/null;then ready=true;break;fi
  sleep 1
 done
 "$ready"
}

sha256sum -c "$manifest"
for command in curl python3 sha256sum mktemp install systemctl;do command -v "$command" >/dev/null;done
test -x "$binary";test -x "$audit";test -f "$source_binary";test -f "$receiver_binary"
source_user=$(systemctl show bitcoinwalk-relay.service --property=User --value)
receiver_user=$(systemctl show bitcoinwalk-replica-rehearsal.service --property=User --value)
test -n "$source_user" && test "$source_user" != root
test -n "$receiver_user" && test "$receiver_user" != root
audit_exact /tmp/bitcoinwalk-replica-moderation.before.json

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-moderation-staging.XXXXXX);chmod 0700 "$backup"
cp -p "$source_binary" "$backup/source-binary.before";cp -p "$receiver_binary" "$backup/receiver-binary.before"
test ! -f "$source_dropin"||cp -p "$source_dropin" "$backup/source-version.before"
test ! -f "$receiver_dropin"||cp -p "$receiver_dropin" "$backup/receiver-version.before"
cp -p /tmp/bitcoinwalk-replica-moderation.before.json "$backup/public.before.json"
source_stopped=false;receiver_stopped=false
cleanup(){
 test "$receiver_stopped" = false||systemctl start bitcoinwalk-replica-rehearsal.service||true
 test "$source_stopped" = false||systemctl start bitcoinwalk-relay.service||true
}
trap cleanup EXIT HUP INT TERM
systemctl stop bitcoinwalk-relay.service;source_stopped=true
systemctl stop bitcoinwalk-replica-rehearsal.service;receiver_stopped=true
for spec in \
 /var/lib/bitcoinwalk-relay/events.db:source-events.db \
 /var/lib/bitcoinwalk-relay/replication-journal.db:source-replication-journal.db \
 /var/lib/bitcoinwalk-replica-rehearsal/events.db:receiver-events.db \
 /etc/bitcoinwalk-replication/registry.json:registry.json \
 /etc/bitcoinwalk-replication/receiver.conf:receiver.conf
do
 src=${spec%%:*};dst=${spec#*:};test ! -f "$src"||cp -p "$src" "$backup/$dst"
done
(cd "$backup" && find . -maxdepth 1 -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
echo "Consistent pre-upgrade source and receiver backup created: $backup"

install -o root -g root -m 0755 "$binary" "$receiver_binary"
install -o root -g root -m 0755 "$binary" "$source_binary"
install -d -o root -g root -m 0755 "$(dirname "$source_dropin")" "$(dirname "$receiver_dropin")"
printf '[Service]\nEnvironment=RELAY_VERSION=bitcoinwalk-organizers-0.8.63\n' >"$source_dropin";chmod 0644 "$source_dropin"
printf '[Service]\nEnvironment=RELAY_VERSION=bitcoinwalk-replica-rehearsal-0.8.63\n' >"$receiver_dropin";chmod 0644 "$receiver_dropin"
systemctl daemon-reload
systemctl reset-failed bitcoinwalk-replica-rehearsal.service bitcoinwalk-relay.service||true
systemctl start bitcoinwalk-replica-rehearsal.service;receiver_stopped=false
wait_service bitcoinwalk-replica-rehearsal.service http://127.0.0.1:3341/healthz
systemctl start bitcoinwalk-relay.service;source_stopped=false
wait_service bitcoinwalk-relay.service http://127.0.0.1:3334/healthz
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3341/ | grep -q 'bitcoinwalk-replica-rehearsal-0.8.63'
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.63'
audit_exact "$backup/public.after.json";cmp "$backup/public.before.json" "$backup/public.after.json"
systemctl restart bitcoinwalk-replica-rehearsal.service bitcoinwalk-relay.service
wait_service bitcoinwalk-replica-rehearsal.service http://127.0.0.1:3341/healthz
wait_service bitcoinwalk-relay.service http://127.0.0.1:3334/healthz
audit_exact "$backup/public.after-restart.json";cmp "$backup/public.before.json" "$backup/public.after-restart.json"
test "$(systemctl show bitcoinwalk-relay.service --property=User --value)" = "$source_user"
test "$(systemctl show bitcoinwalk-replica-rehearsal.service --property=User --value)" = "$receiver_user"
trap - EXIT HUP INT TERM
echo "Replicated walk moderation staging transport 0.8.63 accepted. Backup: $backup"
echo 'The authoritative source and configured Memphis receiver remained exact across activation and restart.'
echo 'Recurring registration and sponsor-logo policy remain included; no moderation decision was created.'
