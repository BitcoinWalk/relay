package main

import (
	"os"
	"strings"
	"testing"
)

func TestLondonCombinedRehearsalPreservesIsolation(t *testing.T) {
	compose, err := os.ReadFile("deploy/compose.london-combined-rehearsal-0.8.98.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	for _, required := range []string{
		`user: "65532:65532"`,
		"read_only: true",
		"no-new-privileges:true",
		"root_my_custom_network",
		`RELAY_CHAT_COMBINED: "true"`,
		"RELAY_CHAT_DB: /data/chat.db",
		"RELAY_CHAT_KEY_FILE: /data/chat-relay-key",
		"RELAY_DB: /data/events.db",
		"wss://london.bitcoinwalk.org",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing combined London rehearsal setting %q", required)
		}
	}
	if strings.Contains(text, "ports:") {
		t.Fatal("combined London rehearsal publishes a host port")
	}
}

func TestLondonCombinedRehearsalIsNonMutating(t *testing.T) {
	data, err := os.ReadFile("deploy/rehearse-london-combined-0.8.98.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		"live-events.before.json",
		"live-events.accepted.json",
		"candidate-event-digest.restart",
		"candidate-chat-digest.restart",
		"combined-chat-probe",
		"chat-relay-key.recovery",
		"docker port",
		"remove_candidate",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing combined London rehearsal gate %q", required)
		}
	}
}

func TestLondonCombinedActivationKeepsSeparateStoresAndRollback(t *testing.T) {
	compose, err := os.ReadFile("deploy/compose.london-combined-0.8.99.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	for _, required := range []string{
		"RELAY_DB: /data/events.db",
		"RELAY_CHAT_DB: /data/chat.db",
		"RELAY_CHAT_KEY_FILE: /data/chat-relay-key",
		"bitcoinwalk-city-community-relay-0.8.99",
		`user: "65532:65532"`,
		"read_only: true",
		"no-new-privileges:true",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing combined London activation setting %q", required)
		}
	}
	if strings.Contains(text, "ports:") {
		t.Fatal("combined London activation publishes a host port")
	}

	data, err := os.ReadFile("deploy/activate-london-combined-0.8.99.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		"chat-relay-key.recovery",
		"event-digest.before",
		"event-digest.restart",
		"chat-digest.restart",
		"events.accepted.json",
		"compose.before.yaml",
		"the accepted event-only relay was restored",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing combined London activation gate %q", required)
		}
	}
}

func TestLondonPublicWalkieTalkieUpgradePreservesEmptyCombinedState(t *testing.T) {
	script, err := os.ReadFile("deploy/upgrade-london-public-walkie-talkie-0.9.0.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"chat-digest.before", "event-digest.before", "chat-relay-key.recovery",
		"combined relay 0.8.99 was restored", "No group was created",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing public Walkie Talkie upgrade gate %q", required)
		}
	}
}
