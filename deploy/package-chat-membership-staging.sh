#!/usr/bin/env bash
set -euo pipefail
[[ $# == 1 && $1 == /* && ! -e $1 ]] || { echo 'Supply a new absolute output directory.' >&2; exit 2; }
output=$1
cd "$(dirname "$0")/.."
mkdir -m 0700 "$output"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o "$output/bitcoinwalk-relay" .
cp deploy/bitcoinwalk-chat-staging.service "$output/staging.service"
cp deploy/install-chat-membership-staging.sh "$output/install-chat-membership-staging.sh"
git rev-parse HEAD > "$output/SOURCE_BASE_COMMIT"
# Retain the exact current source, including the reviewed uncommitted endpoint.
{ git ls-files '*.go' go.mod go.sum 'chat-chooser/*'; printf '%s\n' chat_upload_authorization.go chat_upload_authorization_test.go deploy/install-chat-membership-staging.sh deploy/package-chat-membership-staging.sh deploy/chat_membership_installer_test.go deploy/bitcoinwalk-chat-staging.service; } | sort -u > "$output/source-files.txt"
tar -czf "$output/source.tar.gz" -T "$output/source-files.txt"
(cd "$output" && sha256sum bitcoinwalk-relay staging.service > SHA256SUMS && sha256sum install-chat-membership-staging.sh source.tar.gz SOURCE_BASE_COMMIT SHA256SUMS > PACKAGE-SHA256SUMS)
echo "Staging-only package prepared: $output"
