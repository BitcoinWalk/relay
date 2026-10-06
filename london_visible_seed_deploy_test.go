package main

import (
	"os"
	"strings"
	"testing"
)

func TestLondonVisibleSeedDeployIsScopedAndReadOnly(t *testing.T) {
	data, err := os.ReadFile("deploy/resume-london-visible-seed-0.8.70.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"ca2f9905-fb4d-4948-a12c-c792b28ec7c8", "wss://london.bitcoinwalk.org/", "RELAY_REPLICA_VISIBLE_SEED_CITY", "RELAY_REPLICA_VISIBLE_SEED_PUBLIC_SNAPSHOT", "london-visible-seed-v1", `"delivered":8`, "RELAY_REPLICA_JOURNAL_STABLE_DIGEST"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing London visible seed guard %q", required)
		}
	}
	for _, forbidden := range []string{`cp -p "$original/london-journal.db" "$live_journal"`, `install -m`, "systemctl reload caddy"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("London visible seed contains live activation %q", forbidden)
		}
	}
}
