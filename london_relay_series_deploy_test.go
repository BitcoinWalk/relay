package main

import (
	"os"
	"strings"
	"testing"
)

func TestLondonSeriesReceiverUpgradePreservesHardening(t *testing.T) {
	compose, err := os.ReadFile("deploy/compose.london-relay-0.8.71.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	for _, required := range []string{`user: "65532:65532"`, "read_only: true", "no-new-privileges:true", "root_my_custom_network", "bitcoinwalk-city-relay-0.8.71", "wss://london.bitcoinwalk.org/"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing London hardening %q", required)
		}
	}
	if strings.Contains(text, "ports:") {
		t.Fatal("London series receiver publishes a host port")
	}
}

func TestLondonSeriesUpgradeBacksUpAndVerifiesEmptyState(t *testing.T) {
	data, err := os.ReadFile("deploy/upgrade-london-relay-series-0.8.71.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"readback.before.json", "events.db", "readback.after-restart.json", "container.after.json", "bitcoinwalk-city-relay-0.8.71"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing London upgrade acceptance %q", required)
		}
	}
}
