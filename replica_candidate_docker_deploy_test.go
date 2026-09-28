package main

import (
	"os"
	"strings"
	"testing"
)

func TestReplicaCandidateDockerInstallIsBackupFirstAndAdditive(t *testing.T) {
	data, err := os.ReadFile("deploy/install-replica-candidate-docker-0.8.43.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		`mktemp -d /var/backups/bitcoinwalk-replica-candidate.`,
		`cp -p /root/docker-compose.yml "$backup/root-docker-compose.yml"`,
		`test -z "$(docker port "$container")"`,
		`Not yet present in the signed city directory or live replication registry`,
		`DNS, Nginx Proxy Manager, the live replication registry, the signed directory chain and every pre-existing container were unchanged.`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("candidate installer omitted safety invariant %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl", "caddy", "registry.json", "replica-delivery-key", "docker restart nginx"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("candidate installer crosses isolation boundary through %q", forbidden)
		}
	}
}

func TestReplicaCandidateComposeHasNoSecretOrPublishedPort(t *testing.T) {
	data, err := os.ReadFile("deploy/compose.replica-candidate-0.8.43.yaml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	for _, required := range []string{
		`RELAY_REPLICA_RECEIVER_CONTAINER_LISTEN: "true"`,
		`RELAY_REPLICA_RECEIVER_DESTINATION: wss://replica.bitcoinwalk.org/`,
		`RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY: a6c0c0aba1385505e1e1db704934a67d93fac6c5561d8f256e6dcac1df2ed448`,
		`RELAY_VERSION: bitcoinwalk-replica-candidate-0.8.43`,
		`read_only: true`,
		`root_my_custom_network`,
		`expose:`,
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("candidate Compose omitted invariant %q", required)
		}
	}
	for _, forbidden := range []string{"ports:", "nsec", "private_key", "replica-delivery-key"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("candidate Compose contains forbidden material %q", forbidden)
		}
	}
}
