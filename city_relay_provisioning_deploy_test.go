package main

import (
	"os"
	"strings"
	"testing"
)

func TestCityRelayGatewayComposeIsIsolated(t *testing.T) {
	data, err := os.ReadFile("deploy/compose.city-relay-gateway-0.8.92.yaml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	for _, required := range []string{"read_only: true", `user: "65532:65532"`, "cap_drop:", "no-new-privileges:true", "RELAY_CITY_GATEWAY_REGISTRY", "root_my_custom_network"} {
		if !strings.Contains(compose, required) {
			t.Fatalf("missing gateway guard %q", required)
		}
	}
	for _, forbidden := range []string{"ports:", "privileged: true", "/var/run/docker.sock"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("gateway contains forbidden topology %q", forbidden)
		}
	}
}

func TestPaidCityProvisionerHasFailClosedGates(t *testing.T) {
	data, err := os.ReadFile("deploy/provision-paid-city-relay-0.8.92.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"sha256sum --ignore-missing -c", "approvalEventId", "entitlementId", "evidenceDigest", "readlink -f", "flock", "allow-empty", "ReadonlyRootfs", "docker port", "gateway-registry.before.json", "restart relay", "Public DNS/TLS/WSS"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing provisioning guard %q", required)
		}
	}
	for _, forbidden := range []string{"docker.sock", "privileged: true", "network_mode: host", "MYSQL_ROOT_PASSWORD", "password"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("provisioner contains forbidden capability %q", forbidden)
		}
	}
}

func TestPaidCityWorkerDoesNotExposeCredentialOrRawFailure(t *testing.T) {
	data, err := os.ReadFile("deploy/run-paid-city-relay-worker-0.8.92.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"Authorization: Bearer $token", "bitcoinwalk.org/api/internal/city-relays", "needs-attention", "apply-failed", "exact retry remains safe"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing worker guard %q", required)
		}
	}
	for _, forbidden := range []string{"set -x", "provision.log\" >&2"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("worker exposes forbidden detail %q", forbidden)
		}
	}
}

func TestPaidCityWorkerResumeCreatesDeclaredWritableRoots(t *testing.T) {
	data, err := os.ReadFile("deploy/resume-city-relay-worker-0.8.93.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"install -d -o root -g root -m 0700 /opt/bitcoinwalk-city-relays /var/lib/bitcoinwalk-city-relays", "systemctl reset-failed", "systemctl start bitcoinwalk-city-relay-provisioner.service"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing worker resume guard %q", required)
		}
	}
}

func TestPaidCityWorkerPinsOnlyTheProductionAPIConnection(t *testing.T) {
	data, err := os.ReadFile("deploy/run-paid-city-relay-worker-0.8.94.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"bitcoinwalk.org:443:213.232.235.138", "--resolve \"$resolve\"", "Unexpected production app resolution"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing production endpoint guard %q", required)
		}
	}
	if strings.Contains(script, "/etc/hosts") {
		t.Fatal("worker must not change host-wide name resolution")
	}
}

func TestPaidCityWorkerUsesPrivateWritableDockerConfiguration(t *testing.T) {
	data, err := os.ReadFile("deploy/run-paid-city-relay-worker-0.8.95.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{`export DOCKER_CONFIG="$work/docker"`, `install -d -o root -g root -m 0700 "$DOCKER_CONFIG"`} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing private Docker configuration guard %q", required)
		}
	}
	if strings.Contains(script, "/root/.docker") {
		t.Fatal("worker must not write Docker configuration under the protected root home")
	}
}
