#!/usr/bin/env bash
set -euo pipefail

root_dir=$(cd "$(dirname "$0")/.." && pwd)
cd "$root_dir"

./deploy/recover-production-relay-0.9.6.sh
./deploy/promote-lugano-production-0.9.5.sh
