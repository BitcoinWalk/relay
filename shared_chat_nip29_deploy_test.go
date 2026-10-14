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
		"bitcoinwalk-chat-shared-0.9.1": "e473472bde1474d13fe7a761bf97528b3d3f7ec1a461b5cabf517cd4e9fcb751",
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
	script, err := os.ReadFile("deploy/upgrade-shared-chat-nip29-0.9.1.sh")
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
