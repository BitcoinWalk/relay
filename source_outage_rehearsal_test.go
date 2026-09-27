package main

import (
	"os"
	"strings"
	"testing"
)

func TestSourceOutageRehearsalIsBackupFirstAndReceiverIndependent(t *testing.T) {
	data, err := os.ReadFile("deploy/rehearse-source-outage-0.8.26.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	ordered := []string{
		`sha256sum -c "$manifest"`,
		`backup=$(mktemp -d /var/backups/bitcoinwalk-replica-source-outage.XXXXXX)`,
		`./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/memphis-before.json"`,
		`systemctl stop bitcoinwalk-guide.service bitcoinwalk-relay.service`,
		`cp -p "$source_journal" "$backup/source-replication-journal.db"`,
		`echo "Consistent pre-outage backup created: $backup"`,
		`-baseline "$backup/memphis-before.json"`,
		`-baseline "$backup/nashville-before.json"`,
		`systemctl start bitcoinwalk-relay.service`,
		`cmp "$backup/status-before.json" "$backup/status-after.json"`,
		`echo 'No organizer replication notification was created for source unavailability.'`,
	}
	position := -1
	for _, token := range ordered {
		relative := strings.Index(script[position+1:], token)
		if relative < 0 {
			t.Fatalf("missing or misordered safety step %q", token)
		}
		next := position + 1 + relative
		position = next
	}
	if strings.Contains(script, "systemctl stop bitcoinwalk-replica-rehearsal") || strings.Contains(script, "systemctl stop bitcoinwalk-replica-firstwalk") {
		t.Fatal("source-outage rehearsal stops an independent receiver")
	}
	for _, token := range []string{
		`test "$(systemctl is-active bitcoinwalk-relay.service || true)" = inactive`,
		`grep -q '"reconciled":true' "$file"`,
		`wait_reconciled "$backup/status-after.json"`,
		`test "$(guide_snapshot)" = "$guide_before"`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("missing outage invariant %q", token)
		}
	}
}
