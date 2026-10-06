#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"
manifest=REPLICA-MODERATION-0.8.63-SHA256SUMS
binary="$root_dir/bitcoinwalk-relay-replica-moderation-0.8.63"
audit="$root_dir/replica-audit-shadow-0.8.44"
installed=/opt/bitcoinwalk-relay/bitcoinwalk-relay
dropin=/etc/systemd/system/bitcoinwalk-relay.service.d/90-bw08-version.conf
city=be8514a4-9df0-4159-a517-71f65761cbbe
source=wss://relay-staging.bitcoinwalk.org/
existing=wss://replica-staging.bitcoinwalk.org/
candidate=wss://replica.bitcoinwalk.org/

verify_report(){
 python3 - "$1" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
if not report["readOnly"] or not report["exactEventIds"]:
    raise SystemExit("replica audit is not exact and read-only")
if len(report["source"]["occurrenceIds"]) != 7 or len(report["replica"]["occurrenceIds"]) != 7:
    raise SystemExit("replica audit is not exact 7/7")
for side in ("source", "replica"):
    if not report[side]["allSignaturesValid"] or report[side]["privateWrapperCount"] != 0:
        raise SystemExit("replica signature or privacy acceptance failed")
PY
}

sha256sum -c "$manifest"
for command in curl python3 sha256sum mktemp install systemctl;do command -v "$command" >/dev/null;done
test -x "$binary";test -x "$audit";test -f "$installed"
service_user=$(systemctl show bitcoinwalk-relay.service --property=User --value)
test -n "$service_user" && test "$service_user" != root || { echo 'Refusing to activate a root-run relay.' >&2;exit 1; }
curl --fail --silent --show-error --max-time 5 -H 'Accept: application/nostr+json' https://replica.bitcoinwalk.org/ | grep -q 'bitcoinwalk-replica-candidate-0.8.63'
"$audit" -source "$source" -replica "$candidate" -city "$city" > /tmp/bitcoinwalk-source-0.8.63.candidate.before.json;verify_report /tmp/bitcoinwalk-source-0.8.63.candidate.before.json
"$audit" -source "$source" -replica "$existing" -city "$city" > /tmp/bitcoinwalk-source-0.8.63.existing.before.json;verify_report /tmp/bitcoinwalk-source-0.8.63.existing.before.json

backup=$(mktemp -d /var/backups/bitcoinwalk-replica-moderation-source.XXXXXX);chmod 0700 "$backup"
cp -p "$installed" "$backup/bitcoinwalk-relay.before";test ! -f "$dropin"||cp -p "$dropin" "$backup/version.before"
cp -p /tmp/bitcoinwalk-source-0.8.63.candidate.before.json "$backup/candidate.before.json"
cp -p /tmp/bitcoinwalk-source-0.8.63.existing.before.json "$backup/existing.before.json"
stopped=false
cleanup(){ test "$stopped" = false||systemctl start bitcoinwalk-relay.service||true; }
trap cleanup EXIT HUP INT TERM
systemctl stop bitcoinwalk-relay.service;stopped=true
for name in events.db replication-journal.db;do test ! -f "/var/lib/bitcoinwalk-relay/$name"||cp -p "/var/lib/bitcoinwalk-relay/$name" "$backup/$name";done
test ! -f /etc/bitcoinwalk-replication/registry.json||cp -p /etc/bitcoinwalk-replication/registry.json "$backup/registry.json"
(cd "$backup" && find . -maxdepth 1 -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
echo "Consistent pre-upgrade source backup created: $backup"
install -o root -g root -m 0755 "$binary" "$installed"
printf '[Service]\nEnvironment=RELAY_VERSION=bitcoinwalk-organizers-0.8.63\n' >"$dropin";chmod 0644 "$dropin"
systemctl daemon-reload;systemctl reset-failed bitcoinwalk-relay.service||true;systemctl start bitcoinwalk-relay.service;stopped=false
ready=false;for attempt in $(seq 1 30);do if systemctl is-active --quiet bitcoinwalk-relay.service&&curl --fail --silent --max-time 3 http://127.0.0.1:3334/healthz>/dev/null;then ready=true;break;fi;sleep 1;done
"$ready"||{ echo "Relay health check failed. Backup: $backup" >&2;exit 1; }
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3334/ | grep -q 'bitcoinwalk-organizers-0.8.63'
"$audit" -source "$source" -replica "$candidate" -city "$city" >"$backup/candidate.after.json";verify_report "$backup/candidate.after.json"
"$audit" -source "$source" -replica "$existing" -city "$city" >"$backup/existing.after.json";verify_report "$backup/existing.after.json"
cmp "$backup/candidate.before.json" "$backup/candidate.after.json";cmp "$backup/existing.before.json" "$backup/existing.after.json"
trap - EXIT HUP INT TERM
echo "Replicated walk moderation source 0.8.63 accepted. Backup: $backup"
echo 'Memphis remained exact 7/7 on both receivers; recurring registration and sponsor-logo policy remain included.'
