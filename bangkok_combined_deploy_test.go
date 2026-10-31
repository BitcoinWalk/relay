package main

import (
	"os"
	"strings"
	"testing"
)

func TestBangkokCombinedRehearsalUsesAutomaticReceiverTopology(t *testing.T) {
	compose, err := os.ReadFile("deploy/compose.bangkok-combined-rehearsal-0.9.14.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	for _, required := range []string{
		`user: "65532:65532"`, "read_only: true", "no-new-privileges:true",
		"root_my_custom_network", `RELAY_CHAT_COMBINED: "true"`,
		"RELAY_DB: /data/events.db", "RELAY_CHAT_DB: /data/chat.db",
		"RELAY_CHAT_KEY_FILE: /data/chat-relay-key",
		"d7d1cda8-7a58-42ad-ab60-c4bad61dab9a", "wss://bangkok.bitcoinwalk.org/",
		"/var/lib/bitcoinwalk-city-relays/bangkok-combined-rehearsal:/data",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing Bangkok rehearsal setting %q", required)
		}
	}
	if strings.Contains(text, "ports:") || strings.Contains(strings.ToLower(text), "london") {
		t.Fatal("Bangkok rehearsal publishes a host port or contains London scope")
	}
}

func TestBangkokCombinedActivationIsRollbackSafe(t *testing.T) {
	compose, err := os.ReadFile("deploy/compose.bangkok-combined-0.9.14.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	for _, required := range []string{
		"bitcoinwalk-city-community-relay-bangkok:0.9.14",
		"bitcoinwalk-city-community-relay-0.9.14",
		"/var/lib/bitcoinwalk-city-relays/bangkok:/data",
		"RELAY_DB: /data/events.db", "RELAY_CHAT_DB: /data/chat.db",
		"RELAY_CHAT_KEY_FILE: /data/chat-relay-key", `user: "65532:65532"`,
		"read_only: true", "no-new-privileges:true",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing Bangkok activation setting %q", required)
		}
	}
	if strings.Contains(text, "ports:") || strings.Contains(strings.ToLower(text), "london") {
		t.Fatal("Bangkok activation publishes a host port or contains London scope")
	}

	script, err := os.ReadFile("deploy/activate-bangkok-combined-0.9.14.sh")
	if err != nil {
		t.Fatal(err)
	}
	activation := string(script)
	for _, required := range []string{
		"bitcoinwalk-city-relay:0.8.92", "/opt/bitcoinwalk-city-relays/bangkok",
		"chat-relay-key.recovery", "event-digest.before", "event-digest.restart",
		"chat-digest.restart", "events.accepted.json", "compose.before.yaml",
		"the accepted event-only relay was restored", "rm -f \"$target/bitcoinwalk-relay\"",
	} {
		if !strings.Contains(activation, required) {
			t.Fatalf("missing Bangkok activation gate %q", required)
		}
	}
}

func TestBangkokCombinedRehearsalDoesNotMutatePublicRelay(t *testing.T) {
	script, err := os.ReadFile("deploy/rehearse-bangkok-combined-0.9.14.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"bitcoinwalk-city-relay:0.8.92", "live-events.before.json",
		"live-events.accepted.json", "candidate-event-digest.restart",
		"candidate-chat-digest.restart", "combined-chat-probe",
		"chat-relay-key.recovery", "docker port", "remove_candidate",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing Bangkok rehearsal gate %q", required)
		}
	}
	if strings.Contains(strings.ToLower(text), "london") {
		t.Fatal("Bangkok rehearsal contains London scope")
	}
}
