package main

import (
	"os"
	"strings"
	"testing"
)

func TestSharedChatPublicReadReleaseIsGuarded(t *testing.T) {
	script, err := os.ReadFile("deploy/upgrade-shared-chat-public-read-0.9.5.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"Unexpected installed chat binary",
		"chat.pre-rehearsal.db",
		"RELAY_CHAT_LISTEN=127.0.0.1:3399",
		"chat-visibility-audit-0.9.5",
		"-private-group \"$private_group\"",
		"cmp \"$backup/public.before.json\" \"$backup/candidate.public.json\"",
		"chat.pre-activation.logical-digest",
		"chat.activated.logical-digest",
		"prior binary and database restored",
		"bitcoinwalk-organizers remains membership-only",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("deployment gate is missing %q", required)
		}
	}
	if strings.Contains(text, "cat \"$credential\"") {
		t.Fatal("deployment must never print the relay credential")
	}
}
