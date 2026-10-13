#!/bin/sh
# Claim at most one exact paid-city job from the production app and report only
# bounded acceptance evidence. The bearer token and invoices are never logged.
set -eu

endpoint=${BITCOINWALK_RELAY_PROVISION_ENDPOINT:-https://bitcoinwalk.org/api/internal/city-relays}
token_file=${BITCOINWALK_RELAY_PROVISION_TOKEN_FILE:-/etc/bitcoinwalk-city-relay-provisioner/token}
case "$endpoint" in https://bitcoinwalk.org/api/internal/city-relays) ;; *) echo 'Unexpected provisioning endpoint.' >&2; exit 1;; esac
test -f "$token_file" && test ! -L "$token_file"
test "$(stat -c %a "$token_file")" = 600
token=$(cat "$token_file")
case "$token" in *[!A-Za-z0-9_-]*|'') echo 'Invalid provisioning credential.' >&2; exit 1;; esac
test "${#token}" -ge 43 && test "${#token}" -le 256

work=$(mktemp -d /run/bitcoinwalk-city-relay-worker.XXXXXX)
trap 'rm -rf "$work"' EXIT HUP INT TERM
status=$(curl --silent --show-error --max-time 20 -o "$work/claim.json" -w '%{http_code}' -H "Authorization: Bearer $token" -H 'Accept: application/json' "$endpoint")
test "$status" != 204 || exit 0
test "$status" = 200 || { echo 'Provisioning queue unavailable.' >&2; exit 1; }
python3 - "$work/claim.json" "$work/manifest.json" >"$work/scope" <<'PY'
import json,re,sys
source,target=sys.argv[1:]
with open(source,encoding="utf-8") as handle: data=json.load(handle)
manifest=data.get("manifest")
if not isinstance(manifest,dict) or not isinstance(data.get("leaseUntil"),int): raise SystemExit("invalid worker claim")
city=manifest.get("cityId");digest=manifest.get("evidenceDigest")
if not isinstance(city,str) or not re.fullmatch(r"[0-9a-f-]{36}",city) or not isinstance(digest,str) or not re.fullmatch(r"[0-9a-f]{64}",digest): raise SystemExit("invalid worker scope")
with open(target,"x",encoding="utf-8") as handle: json.dump(manifest,handle,separators=(",",":"));handle.write("\n")
print(city);print(digest)
PY
city=$(sed -n '1p' "$work/scope")
digest=$(sed -n '2p' "$work/scope")

phase=provisioned
failure=
if ! /opt/bitcoinwalk-city-relay-provisioner/deploy/provision-paid-city-relay-0.8.92.sh "$work/manifest.json" >"$work/provision.log" 2>&1; then
  phase=needs-attention
  failure=apply-failed
  diagnostic=/var/backups/bitcoinwalk-city-relay-worker-failure.$(date +%s).$$.log
  install -o root -g root -m 0600 "$work/provision.log" "$diagnostic"
fi
if test "$phase" = provisioned; then
  body=$(printf '{"cityId":"%s","evidenceDigest":"%s","phase":"provisioned","evidence":{"containerNonRoot":true,"readOnly":true,"noPublishedPorts":true,"restartStable":true,"emptyExact":true,"gatewayRouted":true}}' "$city" "$digest")
else
  body=$(printf '{"cityId":"%s","evidenceDigest":"%s","phase":"needs-attention","failureCode":"%s"}' "$city" "$digest" "$failure")
fi
report_status=$(curl --silent --show-error --max-time 20 -o "$work/report.json" -w '%{http_code}' -X POST -H "Authorization: Bearer $token" -H 'Content-Type: application/json' --data "$body" "$endpoint")
test "$report_status" = 200 || { echo 'Provisioning result was not acknowledged; exact retry remains safe.' >&2; exit 1; }
if test "$phase" != provisioned; then
  echo "Paid-city relay provisioning needs operator attention. Private diagnostic: $diagnostic" >&2
  exit 1
fi
echo "Paid-city relay provisioning accepted for $city."
