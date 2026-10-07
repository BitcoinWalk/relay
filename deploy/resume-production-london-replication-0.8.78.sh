#!/usr/bin/env bash
set -euo pipefail

test "$EUID" -eq 0 || { echo 'Run this installer with sudo.' >&2; exit 1; }
test "$#" -eq 1 || { echo "Usage: $0 /var/backups/bitcoinwalk-production-london-replication.BACKUP" >&2; exit 1; }
root_dir=$(cd "$(dirname "$0")/.." && pwd);cd "$root_dir"

manifest=PRODUCTION-LONDON-REPLICATION-RESUME-0.8.78-SHA256SUMS
artifact="$root_dir/bitcoinwalk-relay-production-london-0.8.78"
audit="$root_dir/replica-audit-production-london-0.8.78"
dropin_template="$root_dir/deploy/40-production-london-replication-0.8.77.conf"
version_template="$root_dir/deploy/95-production-london-version-0.8.78.conf"
source=$1
service=bitcoinwalk-relay-production.service
binary=/opt/bitcoinwalk-relay-production/bitcoinwalk-relay
database=/var/lib/bitcoinwalk-relay-production/events.db
journal=/var/lib/bitcoinwalk-relay-production/replication-journal.db
config_dir=/etc/bitcoinwalk-replication-production
registry="$config_dir/registry.json"
ledger="$config_dir/entitlements.json"
dropin=/etc/systemd/system/bitcoinwalk-relay-production.service.d/40-london-replication.conf
version_dropin=/etc/systemd/system/bitcoinwalk-relay-production.service.d/95-london-replication-version.conf
staging_registry=/etc/bitcoinwalk-replication/registry.json
staging_journal=/var/lib/bitcoinwalk-relay/replication-journal.db
city=ca2f9905-fb4d-4948-a12c-c792b28ec7c8
destination=wss://london.bitcoinwalk.org/
expected_evidence=4b54a14253ffcca90eabc01a940781b4539cbdeb45a0147a2e244331e7489672
expected_event_digest=7000b1ac878981235fb75360f3aa9f0c0215e1bfb73e85b2196066853ce8fd4e
port=3360

case "$source" in /var/backups/bitcoinwalk-production-london-replication.*) ;; *) echo 'Unexpected entitlement evidence path.' >&2;exit 1;; esac
test -d "$source" && test ! -L "$source"
test "$(stat -c '%U:%G %a' "$source")" = 'root:root 700'

wait_health(){
 local url=$1 ready=false
 for attempt in $(seq 1 40);do
  if curl --fail --silent --max-time 3 "$url" >/dev/null;then ready=true;break;fi
  sleep 1
 done
 "$ready"
}
verify_audit(){
 python3 - "$1" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: report=json.load(stream)
if not report.get("readOnly") or not report.get("exactEventIds"):
    raise SystemExit("London audit is not exact and read-only")
for side in ("source", "replica"):
    state=report[side]
    if len(state["occurrenceIds"]) != 8 or not state["allSignaturesValid"] or state["privateWrapperCount"] != 0:
        raise SystemExit("London audit is not exact 8/8 public state")
PY
}
audit_public(){
 "$audit" -source wss://relay.bitcoinwalk.org/ -replica "$destination" -city "$city" >"$1"
 verify_audit "$1"
}

sha256sum -c "$manifest"
for command in curl python3 sha256sum mktemp install systemctl journalctl date sed grep stat;do command -v "$command" >/dev/null;done
for file in "$artifact" "$audit" "$dropin_template" "$version_template" "$binary" "$database" "$staging_registry" "$staging_journal";do test -f "$file";done
for name in production-entitlement-authority-key entitlements.json registry.json replication-journal.empty.db entitlement-result.json bootstrap-result.json production-validation.json;do
 test -f "$source/$name" && test ! -L "$source/$name"
done
test -x "$artifact";test -x "$audit"
for unit in "$service" bitcoinwalk-relay.service bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3338/api/healthz >/dev/null
test ! -e "$config_dir";test ! -e "$journal";test ! -e "$dropin";test ! -e "$version_dropin"
! systemctl cat "$service" | grep -q 'RELAY_REPLICA_REGISTRY='

authority=$(RELAY_REPLICA_KEY_PUBLIC="$source/production-entitlement-authority-key" "$artifact")
test "${#authority}" -eq 64
python3 - "$source/entitlement-result.json" "$source/bootstrap-result.json" "$source/production-validation.json" "$city" "$destination" "$expected_evidence" "$expected_event_digest" "$authority" <<'PY'
import json, sys
ent_path, boot_path, validation_path, city, destination, evidence, digest, authority = sys.argv[1:]
with open(ent_path, encoding="utf-8") as stream: ent=json.load(stream)
with open(boot_path, encoding="utf-8") as stream: boot=json.load(stream)
with open(validation_path, encoding="utf-8") as stream: validation=json.load(stream)
if ent.get("cityId") != city or ent.get("status") != "active" or ent.get("evidenceDigest") != evidence or ent.get("authority") != authority:
    raise SystemExit("entitlement evidence does not match the retained authority and payment commitment")
if boot.get("cityId") != city or boot.get("destination") != destination or boot.get("entitlementEventId") != ent.get("entitlementEventId"):
    raise SystemExit("registry bootstrap does not bind the retained entitlement")
if validation.get("eventDigest") != digest or validation.get("eventCount") != 110 or validation.get("paidCities") != [city]:
    raise SystemExit("retained production validation is not the accepted London-only paid state")
PY

backup=$(mktemp -d /var/backups/bitcoinwalk-production-london-replication-resume.XXXXXX);chmod 0700 "$backup"
isolated="$backup/isolated";mkdir "$isolated";chmod 0700 "$isolated"
cp -p "$binary" "$backup/production-binary.before"
cp -p "$database" "$backup/production-events.before.db"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3338/api/healthz >"$backup/app-health.before.json"

isolated_pid=;activated=false;completed=false
stop_isolated(){
 if test -n "$isolated_pid";then kill "$isolated_pid" >/dev/null 2>&1||true;wait "$isolated_pid" >/dev/null 2>&1||true;isolated_pid=;fi
}
recover(){
 code=$?;trap - EXIT HUP INT TERM;stop_isolated
 systemctl reset-failed bitcoinwalk-relay.service >/dev/null 2>&1||true
 systemctl start bitcoinwalk-relay.service >/dev/null 2>&1||true
 if "$activated";then
  systemctl stop "$service" >/dev/null 2>&1||true
  install -o root -g root -m 0755 "$backup/production-binary.before" "$binary"
  rm -f "$dropin" "$version_dropin" "$journal"
  rm -rf "$config_dir"
  systemctl daemon-reload >/dev/null 2>&1||true
 fi
 systemctl reset-failed "$service" >/dev/null 2>&1||true
 systemctl start "$service" >/dev/null 2>&1||true
 if ! "$completed";then echo "Production London replication resume did not complete; accepted production service restored. Backup: $backup" >&2;fi
 exit "$code"
}
trap recover EXIT HUP INT TERM

systemctl stop bitcoinwalk-relay.service
cp -p "$staging_registry" "$backup/staging-registry.before.json"
cp -p "$staging_journal" "$backup/staging-journal.before.db"
systemctl start bitcoinwalk-relay.service
wait_health http://127.0.0.1:3334/healthz
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/staging-journal.before.db" "$artifact" >"$backup/staging-journal.before.digest"

cp -p "$database" "$isolated/events.db"
cp -p "$source/replication-journal.empty.db" "$isolated/journal.db"
env -i PATH=/usr/bin:/bin RELAY_LISTEN="127.0.0.1:$port" RELAY_DB="$isolated/events.db" RELAY_ORGANIZER_MODE=true \
 RELAY_REPLICA_JOURNAL="$isolated/journal.db" RELAY_REPLICA_REGISTRY="$source/registry.json" \
 RELAY_REPLICA_REQUIRE_ENTITLEMENTS=true RELAY_REPLICA_ENTITLEMENT_LEDGER="$source/entitlements.json" \
 RELAY_REPLICA_ENTITLEMENT_AUTHORITY="$authority" RELAY_NAME='BitcoinWalk isolated London production resume' \
 "$artifact" >"$backup/isolated-start.log" 2>&1 &
isolated_pid=$!;wait_health "http://127.0.0.1:$port/healthz";stop_isolated
grep -q 'recovered 10 missing record(s)' "$backup/isolated-start.log"

state_uid=$(stat -Lc %u "$database");state_gid=$(stat -Lc %g "$database")
systemctl stop "$service"
install -d -o root -g root -m 0755 "$config_dir" "$(dirname "$dropin")"
install -o root -g root -m 0644 "$source/registry.json" "$registry"
install -o root -g root -m 0644 "$source/entitlements.json" "$ledger"
install -o "$state_uid" -g "$state_gid" -m 0600 "$source/replication-journal.empty.db" "$journal"
install -o root -g root -m 0755 "$artifact" "$binary"
sed "s/__ENTITLEMENT_AUTHORITY__/$authority/" "$dropin_template" >"$dropin";chown root:root "$dropin";chmod 0644 "$dropin"
install -o root -g root -m 0644 "$version_template" "$version_dropin"
activated=true
systemctl daemon-reload;systemctl reset-failed "$service"||true
activation_since=$(date --iso-8601=seconds)
systemctl start "$service";wait_health http://127.0.0.1:3340/healthz

delivery_ok=false
for attempt in $(seq 1 45);do
 journalctl -u "$service" --since "$activation_since" --no-pager >"$backup/production-activation.log"
 if grep -q 'replica delivery acknowledged 8 queued item(s)' "$backup/production-activation.log";then delivery_ok=true;break;fi
 sleep 2
done
"$delivery_ok"
grep -q 'Replica acceptance journal enabled for 1 operator-configured paid city relay(s); recovered 10 missing record(s)' "$backup/production-activation.log"
audit_public "$backup/london.after.json"

systemctl restart "$service";wait_health http://127.0.0.1:3340/healthz
audit_public "$backup/london.after-restart.json"
cmp "$backup/london.after.json" "$backup/london.after-restart.json"
curl --fail --silent --show-error --max-time 10 -H 'Accept: application/nostr+json' http://127.0.0.1:3340/ >"$backup/nip11.after.json"
grep -q 'bitcoinwalk-production-london-0.8.78' "$backup/nip11.after.json"

systemctl stop bitcoinwalk-relay.service
cp -p "$staging_registry" "$backup/staging-registry.after.json"
cp -p "$staging_journal" "$backup/staging-journal.after.db"
systemctl start bitcoinwalk-relay.service;wait_health http://127.0.0.1:3334/healthz
cmp "$backup/staging-registry.before.json" "$backup/staging-registry.after.json"
RELAY_REPLICA_JOURNAL_STABLE_DIGEST="$backup/staging-journal.after.db" "$artifact" >"$backup/staging-journal.after.digest"
cmp "$backup/staging-journal.before.digest" "$backup/staging-journal.after.digest"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3338/api/healthz >"$backup/app-health.after.json"
cmp "$backup/app-health.before.json" "$backup/app-health.after.json"
for unit in "$service" bitcoinwalk-relay.service bitcoinwalk-guide.service;do systemctl is-active --quiet "$unit";done

(cd "$backup" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS)
completed=true;trap - EXIT HUP INT TERM
echo "London production entitlement and registry resume accepted on 0.8.78. Backup: $backup"
echo 'The retained signed entitlement was reused; London remained exact 8/8 and eight queued public envelopes were acknowledged.'
echo 'The stable staging registry and journal, application, Guide and signed directory chain remained unchanged.'
echo "Preserve an offline recovery copy of $source/production-entitlement-authority-key."
