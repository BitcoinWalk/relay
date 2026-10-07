package main

import (
	"os"
	"strings"
	"testing"
)

func TestProductionPromotionActivationGuards(t *testing.T) {
	data, err := os.ReadFile("deploy/activate-production-promotion-0.8.76.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		"7000b1ac878981235fb75360f3aa9f0c0215e1bfb73e85b2196066853ce8fd4e",
		"ca2f9905-fb4d-4948-a12c-c792b28ec7c8",
		"eventCount\") != 110",
		"len(report.get(\"freeCities\", [])) != 11",
		"production-events.before.db",
		"production-binary.before",
		"production.candidate.db",
		"RELAY_VERSION=bitcoinwalk-production-0.8.76",
		"install -o root -g root -m 0755 \"$artifact\" \"$production_binary\"",
		"audit_all ws://127.0.0.1:3340",
		"systemctl restart \"$production_service\"",
		"test ! -e /etc/bitcoinwalk-replication-production",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("production promotion activation lacks guard %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl reload caddy", "systemctl restart caddy", "resolvectl", "/etc/bitcoinwalk-replication/registry.json"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("production promotion activation contains forbidden operation %q", forbidden)
		}
	}
}
