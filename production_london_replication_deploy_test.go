package main

import (
	"os"
	"strings"
	"testing"
)

func TestProductionLondonReplicationActivationGuards(t *testing.T) {
	data, err := os.ReadFile("deploy/activate-production-london-replication-0.8.73.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		"ca2f9905-fb4d-4948-a12c-c792b28ec7c8",
		"wss://london.bitcoinwalk.org/",
		"bitcoinwalk-paid-city-payment-v1:",
		"4b54a14253ffcca90eabc01a940781b4539cbdeb45a0147a2e244331e7489672",
		"entitlement-production-issue-v1",
		"entitlement-production-bootstrap-v1",
		"RELAY_REPLICA_REQUIRE_ENTITLEMENTS=true",
		"recovered 10 missing record(s)",
		"replica delivery acknowledged 8 queued item(s)",
		"len(state[\"occurrenceIds\"]) != 8",
		"staging-journal.before.digest",
		"production-entitlement-authority-key",
		"LoadCredential=replica-delivery-key",
	} {
		if !strings.Contains(script+mustReadDeployFile(t, "deploy/40-production-london-replication-0.8.73.conf"), required) {
			t.Fatalf("production London activation lacks guard %q", required)
		}
	}
	for _, forbidden := range []string{
		"paymentHash\")\nprint",
		"/etc/bitcoinwalk-replication/registry.json\" >",
		"systemctl restart caddy",
		"systemctl reload caddy",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("production London activation contains forbidden operation %q", forbidden)
		}
	}
}

func mustReadDeployFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
