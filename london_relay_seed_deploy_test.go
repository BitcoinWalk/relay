package main

import (
	"os"
	"strings"
	"testing"
)

func TestLondonSeedUsesOnlyIsolatedRegistryAndJournal(t *testing.T) {
	data, err := os.ReadFile("deploy/seed-london-relay-0.8.69.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"ca2f9905-fb4d-4948-a12c-c792b28ec7c8", "wss://london.bitcoinwalk.org/", `RELAY_REPLICA_JOURNAL="$journal"`, `RELAY_REPLICA_REGISTRY="$registry"`, "recovered 8 missing record(s)", "replica delivery acknowledged 8 queued item(s)", "RELAY_REPLICA_RECONCILIATION_AUDIT=true", "RELAY_REPLICA_JOURNAL_STABLE_DIGEST"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing London seed guard %q", required)
		}
	}
	for _, forbidden := range []string{`install -m`, `cp -p "$registry" "$live_registry"`, `cp -p "$journal" "$live_journal"`, "systemctl reload caddy"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("London seed contains live activation %q", forbidden)
		}
	}
}
