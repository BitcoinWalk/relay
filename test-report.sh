#!/bin/sh
# Consistent RAG test report for handoffs. Counts top-level Go tests discovered
# by the toolchain; subtests remain included in execution but not double-counted.
set -u

go_cmd=${GO_CMD:-go}
count=$($go_cmd test -buildvcs=false -list '^Test' ./... 2>/dev/null | awk '/^Test/{n++} END{print n+0}')
packages=$($go_cmd list -buildvcs=false ./... | wc -l | tr -d ' ')
python_count=$(python3 -c "import unittest; print(unittest.defaultTestLoader.discover('deploy', pattern='*_test.py').countTestCases())")
failed=0

printf 'Discovered: %s top-level tests across %s packages\n' "$count" "$packages"
if $go_cmd test -buildvcs=false ./... -count=1; then
 printf '🟢 Full suite: succeeded (%s discovered tests)\n' "$count"
else
 printf '🔴 Full suite: failed (%s discovered tests)\n' "$count"
 failed=1
fi
if python3 -m unittest discover -s deploy -p '*_test.py'; then
 printf '🟢 Python deployment verifiers: succeeded (%s tests)\n' "$python_count"
else
 printf '🔴 Python deployment verifiers: failed (%s tests)\n' "$python_count"
 failed=1
fi
if $go_cmd test -buildvcs=false -race ./... -count=1; then
 printf '🟢 Race detector: succeeded\n'
else
 printf '🔴 Race detector: failed\n'
 failed=1
fi
if $go_cmd vet -buildvcs=false ./...; then
 printf '🟢 Static analysis: succeeded\n'
else
 printf '🔴 Static analysis: failed\n'
 failed=1
fi

if [ "$failed" -eq 0 ]; then
 printf '🟢 Overall: succeeded\n'
else
 printf '🟠 Overall: partially succeeded; inspect red checks above\n'
fi
exit "$failed"
