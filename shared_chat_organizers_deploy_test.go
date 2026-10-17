package main

import (
	"os"
	"strings"
	"testing"
)

func TestSharedChatOrganizerReleaseIsGuarded(t *testing.T) {
	script, err := os.ReadFile("deploy/upgrade-shared-chat-organizers-0.9.4.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"Unexpected installed chat binary",
		"Unexpected chat service contract",
		"Unexpected public chat configuration",
		"chat.pre-rehearsal.db",
		"RELAY_CHAT_LISTEN=127.0.0.1:3399",
		"cmp \"$backup/public.before.json\" \"$backup/candidate.public.json\"",
		"chat.pre-activation.logical-digest",
		"chat.activated.logical-digest",
		"Organizer-room relay update failed",
		"Only bitcoinwalk-organizers may use private, restricted and closed metadata",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("deployment gate is missing %q", required)
		}
	}
	if strings.Contains(text, "cat \"$credential\"") {
		t.Fatal("deployment must never print the relay credential")
	}
}
