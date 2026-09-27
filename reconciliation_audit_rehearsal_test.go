package main

import (
	"os"
	"strings"
	"testing"
)

func TestReconciliationAuditRehearsalIsBackupFirstAndNonInstalling(t *testing.T) {
	data, err := os.ReadFile("deploy/rehearse-reconciliation-audit-0.8.33.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	required := []string{
		`systemctl stop bitcoinwalk-guide.service bitcoinwalk-relay.service`,
		`cp -p "$source_db" "$backup/source-events.db"`,
		`cp -p "$source_journal" "$backup/source-replication-journal.db"`,
		`RELAY_REPLICA_RECONCILIATION_AUDIT=true`,
		`test "$events_before" = "$events_after"`,
		`test "$journal_before" = "$journal_after"`,
		`cmp "$backup/reconciliation-1.json" "$backup/reconciliation-2.json"`,
		`cmp "$backup/memphis-before.json" "$backup/memphis-after.json"`,
		`cmp "$backup/nashville-before.json" "$backup/nashville-after.json"`,
		`test "$(guide_snapshot)" = "$guide_before"`,
		`No audit copy or new executable was installed into a live path.`,
	}
	for _, invariant := range required {
		if !strings.Contains(script, invariant) {
			t.Fatalf("rehearsal omitted safety invariant %q", invariant)
		}
	}
	for _, forbidden := range []string{"cp -p \"$artifact\" \"$source_target\"", "mv \"$artifact\"", "install -m"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("rehearsal installs live code through %q", forbidden)
		}
	}
}
