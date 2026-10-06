package main

import (
	"os"
	"strings"
	"testing"
)

func TestRemoteSignerDeploymentIsIsolatedAndEphemeral(t *testing.T) {
	unit, err := os.ReadFile("deploy/bitcoinwalk-remote-signer.service")
	if err != nil {
		t.Fatal(err)
	}
	service := string(unit)
	for _, required := range []string{
		"DynamicUser=yes",
		"RELAY_SIGNER_MODE=remote",
		"RELAY_SIGNER_LISTEN=127.0.0.1:3344",
		"RELAY_SIGNER_SERVICE_URL=wss://remote.bitcoinwalk.org/",
		"ProtectSystem=strict",
		"MemoryMax=128M",
	} {
		if !strings.Contains(service, required) {
			t.Fatalf("service missing %q", required)
		}
	}
	if strings.Contains(service, "StateDirectory") || strings.Contains(service, "RELAY_DB") {
		t.Fatal("remote signer must not configure persistent storage")
	}

	proxy, err := os.ReadFile("deploy/Caddyfile.remote-signer")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"remote.bitcoinwalk.org", "127.0.0.1:3344", "max_size 64KB"} {
		if !strings.Contains(string(proxy), required) {
			t.Fatalf("proxy missing %q", required)
		}
	}
}
