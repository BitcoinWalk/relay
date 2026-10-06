package main

import (
	"os"
	"strings"
	"testing"
)

func TestReplicaModerationCandidateUpgradePreservesIsolation(t *testing.T) {
	composeData, err := os.ReadFile("deploy/compose.replica-candidate-0.8.63.yaml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(composeData)
	for _, required := range []string{
		`RELAY_REPLICA_RECEIVER_CONTAINER_LISTEN: "true"`,
		`RELAY_REPLICA_RECEIVER_DESTINATION: wss://replica.bitcoinwalk.org/`,
		`RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY: a6c0c0aba1385505e1e1db704934a67d93fac6c5561d8f256e6dcac1df2ed448`,
		`RELAY_VERSION: bitcoinwalk-replica-candidate-0.8.63`,
		`read_only: true`,
		`user: "65532:65532"`,
		`no-new-privileges:true`,
		`root_my_custom_network`,
		`tmpfs:`,
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

	scriptData, err := os.ReadFile("deploy/upgrade-replica-candidate-moderation-0.8.63.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptData)
	for _, required := range []string{
		`mktemp -d /var/backups/bitcoinwalk-replica-moderation-candidate.`,
		`docker build --pull=false --network=none`,
		`test -z "$(docker port "$container")"`,
		`cmp "$backup/candidate.before.json" "$backup/candidate.after.json"`,
		`cmp "$backup/candidate.before.json" "$backup/candidate.after-restart.json"`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("candidate upgrade omitted safety invariant %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl", "Caddy", "registry.json", "replica-delivery-key"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("candidate upgrade crosses isolation boundary through %q", forbidden)
		}
	}
}

func TestReplicaModerationStagingUpgradeIsBackupFirstAndNonRoot(t *testing.T) {
	data, err := os.ReadFile("deploy/install-replica-moderation-staging-0.8.63.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		`mktemp -d /var/backups/bitcoinwalk-replica-moderation-staging.`,
		`test -n "$source_user" && test "$source_user" != root`,
		`test -n "$receiver_user" && test "$receiver_user" != root`,
		`systemctl stop bitcoinwalk-replica-rehearsal.service`,
		`systemctl stop bitcoinwalk-relay.service`,
		`RELAY_VERSION=bitcoinwalk-organizers-0.8.63`,
		`RELAY_VERSION=bitcoinwalk-replica-rehearsal-0.8.63`,
		`cmp "$backup/public.before.json" "$backup/public.after.json"`,
		`cmp "$backup/public.before.json" "$backup/public.after-restart.json"`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("source upgrade omitted safety invariant %q", required)
		}
	}
	if strings.Contains(script, "install -o") && strings.Contains(script, "events.db\" \"/var/lib") {
		t.Fatal("source upgrade contains an automatic database restore")
	}
}
