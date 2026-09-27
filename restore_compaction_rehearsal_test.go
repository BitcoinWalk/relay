package main

import (
	"os"
	"strings"
	"testing"
)

func TestRestoreCompactionRehearsalNeverReplacesLiveState(t *testing.T) {
	data, err := os.ReadFile("deploy/rehearse-restore-compaction-0.8.27.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	ordered := []string{
		`sha256sum -c "$manifest"`,
		`backup=$(mktemp -d /var/backups/bitcoinwalk-replica-restore-compaction.XXXXXX)`,
		`./replica-audit-0.8.26 -source ws://127.0.0.1:3334 -replica ws://127.0.0.1:3341 >"$backup/memphis-before.json"`,
		`systemctl stop bitcoinwalk-guide.service bitcoinwalk-replica-firstwalk.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-relay.service`,
		`cp -p "$source_db" "$backup/source-events.db"`,
		`cp -p "$source_journal" "$backup/source-replication-journal.db"`,
		`cp -p "$memphis_db" "$backup/memphis-events.db"`,
		`cp -p "$nashville_db" "$backup/nashville-events.db"`,
		`echo "Consistent pre-compaction backup created: $backup"`,
		`systemctl start bitcoinwalk-relay.service bitcoinwalk-replica-rehearsal.service bitcoinwalk-replica-firstwalk.service bitcoinwalk-guide.service`,
		`compact "$backup/source-events.db" "$restore/source-events.db"`,
		`compact "$backup/source-replication-journal.db" "$restore/source-replication-journal.db"`,
		`compact "$backup/memphis-events.db" "$restore/memphis-events.db"`,
		`compact "$backup/nashville-events.db" "$restore/nashville-events.db"`,
		`RELAY_LISTEN=127.0.0.1:3354`,
		`RELAY_LISTEN=127.0.0.1:3355`,
		`RELAY_LISTEN=127.0.0.1:3356`,
		`-source ws://127.0.0.1:3354 -replica ws://127.0.0.1:3355`,
		`-source ws://127.0.0.1:3354 -city "$nashville" -replica ws://127.0.0.1:3356 -allow-empty`,
		`audit_equal "$backup/memphis-before.json" "$backup/memphis-restored.json"`,
		`audit_equal "$backup/nashville-before.json" "$backup/nashville-restored.json"`,
		`test "$(guide_snapshot)" = "$guide_before"`,
	}
	position := -1
	for _, token := range ordered {
		relative := strings.Index(script[position+1:], token)
		if relative < 0 {
			t.Fatalf("missing or misordered recovery step %q", token)
		}
		position += 1 + relative
	}
	for _, forbidden := range []string{
		`mv "$restore/`,
		`cp "$restore/source-events.db" "$source_db"`,
		`cp "$restore/memphis-events.db" "$memphis_db"`,
		`cp "$restore/nashville-events.db" "$nashville_db"`,
		`RELAY_REPLICA_DELIVERY_KEY_FILE`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("rehearsal contains forbidden live-state or delivery operation %q", forbidden)
		}
	}
	for _, required := range []string{
		`chmod 0700 "$backup" "$restore"`,
		`trap recover EXIT HUP INT TERM`,
		`kill_isolated`,
		`digest_equal "$backup/source-events.db" "$restore/source-events.db"`,
		`digest_equal "$backup/source-replication-journal.db" "$restore/source-replication-journal.db"`,
		`test "$(systemctl is-active bitcoinwalk-relay.service)" = active`,
		`echo 'No compacted database was installed into a live path.'`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing recovery invariant %q", required)
		}
	}
}
