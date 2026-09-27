#!/bin/sh
# Persist the audited Memphis trust anchor and owner-signed offline snapshot.
# Public-relay reads are optional attestations, never activation dependencies.
# This does not replace or restart any live service.
set -eu

test "$(id -u)" -eq 0 || { echo 'Run with sudo.' >&2; exit 1; }
test "$#" -eq 0 || { echo "Usage: $0" >&2; exit 1; }
cd "$(dirname "$0")/.."

manifest=CITY-DIRECTORY-ANCHOR-0.8.34-SHA256SUMS
audit=./bitcoinwalk-city-directory-audit-0.8.34
collector=./deploy/capture-city-directory-root-0.8.34.mjs
anchors=./deploy/city-directory-anchors-0.8.34.json
bundle=./deploy/city-directory-memphis-root-0.8.34.json
node=/opt/bitcoinwalk-app-staging/runtime/bin/node
city=be8514a4-9df0-4159-a517-71f65761cbbe
root_event=d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79
owner=4506e04e4b7079ce07e38e9875678a81ad33a456c696d708ef8e9a2d8c16ba04
anchor_dir=/etc/bitcoinwalk-relay/city-directory
mirror_dir=/var/lib/bitcoinwalk-relay/city-directory-mirrors
libexec_dir=/usr/local/libexec/bitcoinwalk
anchor_target=$anchor_dir/anchors.json
bundle_target=$mirror_dir/memphis-bundled.json
damus_target=$mirror_dir/damus.json
nos_target=$mirror_dir/nos.json
status_target=$mirror_dir/attestations.json
audit_target=$libexec_dir/bitcoinwalk-city-directory-audit
collector_target=$libexec_dir/capture-city-directory-root.mjs

sha256sum -c "$manifest"
test -x "$audit"; test -x "$collector"; test -x "$node"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy; do systemctl is-active --quiet "$unit"; done

backup=$(mktemp -d /var/backups/bitcoinwalk-city-directory-anchor.XXXXXX)
candidate=$backup/candidate
mkdir "$candidate"
chmod 0700 "$backup" "$candidate"
installed=0
anchor_dir_created=0
mirror_dir_created=0
libexec_dir_created=0

backup_target() {
 target=$1; label=$2
 if test -e "$target"; then
  test -f "$target" && test ! -L "$target"
  cp -p "$target" "$backup/$label"
 else
  : >"$backup/$label.absent"
 fi
}
restore_target() {
 target=$1; label=$2
 if test -f "$backup/$label.absent"; then rm -f "$target"; else cp -p "$backup/$label" "$target"; fi
}
recover() {
 code=$?; trap - EXIT HUP INT TERM
 if test "$installed" -eq 1; then
  restore_target "$anchor_target" anchors.json
  restore_target "$bundle_target" bundle.json
  restore_target "$damus_target" damus.json
  restore_target "$nos_target" nos.json
  restore_target "$status_target" attestations.json
  restore_target "$audit_target" audit
  restore_target "$collector_target" collector
  test "$anchor_dir_created" -eq 0 || rmdir "$anchor_dir" 2>/dev/null || true
  test "$mirror_dir_created" -eq 0 || rmdir "$mirror_dir" 2>/dev/null || true
  test "$libexec_dir_created" -eq 0 || rmdir "$libexec_dir" 2>/dev/null || true
 fi
 echo "City directory anchor activation did not complete; prior files were restored. Backup: $backup" >&2
 exit "$code"
}
trap recover EXIT HUP INT TERM

backup_target "$anchor_target" anchors.json
backup_target "$bundle_target" bundle.json
backup_target "$damus_target" damus.json
backup_target "$nos_target" nos.json
backup_target "$status_target" attestations.json
backup_target "$audit_target" audit
backup_target "$collector_target" collector
echo "Consistent pre-activation anchor and audit backup created: $backup"

install -m 0644 "$anchors" "$candidate/anchors.json"
install -m 0644 "$bundle" "$candidate/bundle.json"
capture() {
 relay=$1; output=$2; attempt=1
 while ! "$node" "$collector" "$relay" "$root_event" >"$output.tmp"; do
  rm -f "$output.tmp"
  test "$attempt" -lt 4 || return 1
  echo "Directory root read from $relay has not converged; retrying in 5 seconds." >&2
  attempt=$((attempt+1)); sleep 5
 done
 chmod 0644 "$output.tmp"
 mv "$output.tmp" "$output"
}
damus_status=unavailable
nos_status=unavailable
attestations=
attestation_count=0
if capture wss://relay.damus.io/ "$candidate/damus.json"; then
 damus_status=verified
 attestations=$candidate/damus.json
 attestation_count=$((attestation_count+1))
else
 echo 'Damus attestation unavailable; continuing with the signed offline bundle.' >&2
fi
if capture wss://nos.lol/ "$candidate/nos.json"; then
 nos_status=verified
 attestation_count=$((attestation_count+1))
 if test -n "$attestations"; then attestations=$attestations,$candidate/nos.json; else attestations=$candidate/nos.json; fi
else
 echo 'nos.lol attestation unavailable; continuing with the signed offline bundle.' >&2
fi
printf '{"version":1,"damus":"%s","nos":"%s"}\n' "$damus_status" "$nos_status" >"$candidate/attestations.json"
chmod 0644 "$candidate/attestations.json"

run_audit() {
 env -i PATH=/usr/bin:/bin \
  RELAY_CITY_DIRECTORY_AUDIT_ANCHORS="$1" \
  RELAY_CITY_DIRECTORY_AUDIT_BUNDLE="$2" \
  RELAY_CITY_DIRECTORY_AUDIT_MIRRORS="$3" \
  RELAY_CITY_DIRECTORY_AUDIT_CITY="$city" "$4"
}
run_audit "$candidate/anchors.json" "$candidate/bundle.json" "$attestations" "$audit" >"$candidate/audit.json"
"$node" -e '
const fs=require("node:fs");const x=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
const expected={version:1,cityId:process.argv[2],currentEventId:process.argv[3],sequence:0,ownerPubkey:process.argv[4],chainLength:1,bundleVerified:true,attestationCount:Number(process.argv[5])};
for(const [key,value] of Object.entries(expected))if(x[key]!==value)throw new Error(`unexpected ${key}`);
if(x.operatorPubkeys.length!==0||x.recoveryPubkeys.length!==1||x.publicRelays.length!==1)throw new Error("unexpected directory authority or endpoints");
' "$candidate/audit.json" "$city" "$root_event" "$owner" "$attestation_count"

test -d "$anchor_dir" || { mkdir -p "$anchor_dir"; anchor_dir_created=1; }
test -d "$mirror_dir" || { mkdir -p "$mirror_dir"; mirror_dir_created=1; }
test -d "$libexec_dir" || { mkdir -p "$libexec_dir"; libexec_dir_created=1; }
installed=1
install -m 0644 "$candidate/anchors.json" "$anchor_target"
install -m 0644 "$candidate/bundle.json" "$bundle_target"
install -m 0644 "$candidate/attestations.json" "$status_target"
installed_attestations=
if test "$damus_status" = verified; then
 install -m 0644 "$candidate/damus.json" "$damus_target"
 installed_attestations=$damus_target
fi
if test "$nos_status" = verified; then
 install -m 0644 "$candidate/nos.json" "$nos_target"
 if test -n "$installed_attestations"; then installed_attestations=$installed_attestations,$nos_target; else installed_attestations=$nos_target; fi
fi
install -m 0755 "$audit" "$audit_target"
install -m 0755 "$collector" "$collector_target"
run_audit "$anchor_target" "$bundle_target" "$installed_attestations" "$audit_target" >"$backup/audit-installed.json"
cmp "$candidate/audit.json" "$backup/audit-installed.json"
for unit in bitcoinwalk-guide bitcoinwalk-app-staging bitcoinwalk-relay bitcoinwalk-replica-rehearsal bitcoinwalk-replica-firstwalk caddy; do systemctl is-active --quiet "$unit"; done
(
 cd "$backup"
 sha256sum candidate/anchors.json candidate/bundle.json candidate/attestations.json candidate/audit.json audit-installed.json >SHA256SUMS
 test "$damus_status" != verified || sha256sum candidate/damus.json >>SHA256SUMS
 test "$nos_status" != verified || sha256sum candidate/nos.json >>SHA256SUMS
)

installed=2
trap - EXIT HUP INT TERM
echo "Memphis city-directory anchor accepted from the owner-signed offline bundle. Backup: $backup"
echo "Root event: $root_event"
echo "Optional public attestations verified: $attestation_count/2 (Damus: $damus_status; nos.lol: $nos_status)."
echo 'Strict signature, pinned root, chain, authority and endpoint validation passed without depending on a public relay.'
echo 'No live relay binary, registry, journal, event database, app, Guide, DNS or Caddy state was changed.'
