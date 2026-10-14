package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSharedChatNIP29ReleaseIsGuarded(t *testing.T) {
	want := map[string]string{
		"bitcoinwalk-chat-shared-0.9.2": "08d214ff14600754eef76eb04ad9ad95092d9735ddc5d64307ae87b8d120c99a",
		"chat-public-audit-0.9.1":       "80b42ccd463d9f5d901378dac0d793006f09876e2420316351be004cd06dcb89",
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
	script, err := os.ReadFile("deploy/upgrade-shared-chat-nip29-0.9.3.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"Unexpected installed chat binary",
		"dd7a61a18b9cc6052b359e858116fabf6e34707ba92b45d39ccd5a30a9fcea86",
		"Public chat URL setting is missing or duplicated",
		"grep -Fx 'RELAY_CHAT_URL=wss://chat.bitcoinwalk.org'",
		"chat.pre-rehearsal.db",
		"RELAY_CHAT_LISTEN=127.0.0.1:3399",
		"cmp \"$backup/public.before.json\" \"$backup/candidate.public.json\"",
		"chat.pre-activation.db",
		"chat.pre-activation.logical-digest",
		"chat.activated.logical-digest",
		"Shared Basic-city NIP-29 relay identity accepted",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("deployment gate is missing %q", required)
		}
	}
	if strings.Contains(text, "cat \"$credential\"") {
		t.Fatal("deployment must never print the relay credential")
	}
}
