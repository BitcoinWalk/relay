package main

import (
	"os"
	"strings"
	"testing"
)

func TestProductionLondonReplicationResumeGuards(t *testing.T) {
	data, err := os.ReadFile("deploy/resume-production-london-replication-0.8.78.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		"RELAY_REPLICA_KEY_PUBLIC",
		"production-entitlement-authority-key",
		"entitlement-result.json",
		"replication-journal.empty.db",
		"recovered 10 missing record(s)",
		"for attempt in $(seq 1 45)",
		"replica delivery acknowledged 8 queued item(s)",
		"staging-journal.before.db",
		"staging-journal.after.db",
		"bitcoinwalk-production-london-0.8.78",
	} {
		if !strings.Contains(script+mustReadDeployFile(t, "deploy/95-production-london-version-0.8.78.conf"), required) {
			t.Fatalf("production London resume lacks guard %q", required)
		}
	}
	for _, forbidden := range []string{"RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_KEY", "systemctl reload caddy", "systemctl restart caddy"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("production London resume contains forbidden operation %q", forbidden)
		}
	}
}
