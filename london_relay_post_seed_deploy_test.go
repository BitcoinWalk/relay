package main

import (
	"os"
	"strings"
	"testing"
)

func TestLondonPostSeedBackupRequiresExactRestartStableState(t *testing.T) {
	data, err := os.ReadFile("deploy/backup-restart-london-relay-0.8.72.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"wss://relay-staging.bitcoinwalk.org/", "wss://london.bitcoinwalk.org/", "len(expected) != 8", "events.db", "readback.after-start.json", "readback.after-restart.json", `User"] != "65532:65532"`, "PortBindings"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing London post-seed acceptance %q", required)
		}
	}
	for _, forbidden := range []string{"registry.json", "Caddyfile", "docker compose down"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("post-seed acceptance contains activation operation %q", forbidden)
		}
	}
}
