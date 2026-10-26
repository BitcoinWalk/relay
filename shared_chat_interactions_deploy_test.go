package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSharedChatInteractionsReleaseIsGuarded(t *testing.T) {
	want := map[string]string{
		"bitcoinwalk-chat-shared-0.9.5":                    "db5efeee38bb9199b0ed906ea82bc096c2c57dc1941df0342f1d43d10d4c4797",
		"chat-public-audit-0.9.5":                          "3f838ab9ff7d87759932514eaedf327130fd04dc122e795d591bd27b21d54c5c",
		"SHARED-CHAT-INTERACTIONS-0.9.5-README.txt":        "37b7be119966389e5c81217e0a29e7d5ee25eba4d373c021dbe970a0a108798c",
		"deploy/upgrade-shared-chat-interactions-0.9.5.sh": "2bb94329d2d2556b91589addad355d5fcc59ee33469dafbd4bc0f5b77201b4c2",
	}
	for path, expected := range want {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != expected {
			t.Fatalf("%s digest = %s", path, got)
		}
	}
	script, err := os.ReadFile("deploy/upgrade-shared-chat-interactions-0.9.5.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"Unexpected installed chat binary",
		"chat.pre-rehearsal.db",
		"RELAY_CHAT_LISTEN=127.0.0.1:3399",
		"cmp \"$backup/public.before.json\" \"$backup/candidate.public.json\"",
		"chat.pre-activation.db",
		"chat.pre-activation.logical-digest",
		"chat.activated.logical-digest",
		"Shared chat interactions 0.9.5 accepted",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("deployment gate is missing %q", required)
		}
	}
	if strings.Contains(text, "cat \"$credential\"") {
		t.Fatal("deployment must never print the relay credential")
	}
}
