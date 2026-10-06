package main

import (
	"os"
	"strings"
	"testing"
)

func TestLondonRelayComposeIsCityScopedAndPrivate(t *testing.T) {
	data, err := os.ReadFile("deploy/compose.london-relay-0.8.68.yaml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	for _, required := range []string{
		`container_name: bitcoinwalk-relay-london`,
		`read_only: true`,
		`user: "65532:65532"`,
		`RELAY_REPLICA_RECEIVER_CITY: ca2f9905-fb4d-4948-a12c-c792b28ec7c8`,
		`RELAY_REPLICA_RECEIVER_DESTINATION: wss://london.bitcoinwalk.org/`,
		`RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY: a6c0c0aba1385505e1e1db704934a67d93fac6c5561d8f256e6dcac1df2ed448`,
		`root_my_custom_network`,
		`/tmp:rw,noexec,nosuid,nodev,size=32m`,
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("missing London receiver guard %q", required)
		}
	}
	for _, forbidden := range []string{"ports:", "network_mode: host", "privileged: true"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("London receiver exposes forbidden topology %q", forbidden)
		}
	}
}

func TestLondonRelayInstallerDoesNotActivatePublicRouting(t *testing.T) {
	data, err := os.ReadFile("deploy/install-london-relay-docker-0.8.68.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"sha256sum -c", "Consistent pre-activation Docker topology backup", "audit_empty", "allow-empty", "ReadonlyRootfs", "docker port", "containers.before", "containers.after"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing London installer guard %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl reload caddy", "resolvectl", "registry.json", "directory-staging.bitcoinwalk.org"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("London installer contains public activation %q", forbidden)
		}
	}
}
