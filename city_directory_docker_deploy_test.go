package main

import (
	"os"
	"strings"
	"testing"
)

func TestSecondaryDirectoryDockerDeploymentIsIsolatedAndBackupFirst(t *testing.T) {
	compose, err := os.ReadFile("deploy/compose.directory-2-staging-0.8.40.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile("deploy/Dockerfile.directory-2-staging")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"container_name: bitcoinwalk-directory-2-staging",
		"read_only: true",
		"user: \"65532:65532\"",
		"cap_drop:",
		"no-new-privileges:true",
		"expose:",
		"- \"3343\"",
		"RELAY_CITY_DIRECTORY_CONTAINER_LISTEN: \"true\"",
		"RELAY_NAME: BitcoinWalk secondary staging city-directory transport",
		"/var/lib/bitcoinwalk-directory-2-staging:/data",
		"name: root_my_custom_network",
	} {
		if !strings.Contains(string(compose), required) {
			t.Fatalf("secondary directory compose missing %q", required)
		}
	}
	for _, forbidden := range []string{"/var/run/docker.sock", "privileged: true", "network_mode: host", "213.232.235.240", "ports:", "127.0.0.1:3343"} {
		if strings.Contains(string(compose), forbidden) {
			t.Fatalf("secondary directory compose contains unsafe coupling %q", forbidden)
		}
	}
	if !strings.Contains(string(dockerfile), "FROM scratch") || !strings.Contains(string(dockerfile), "USER 65532:65532") {
		t.Fatal("secondary directory image is not scratch-based and non-root")
	}
	installer, err := os.ReadFile("deploy/install-directory-2-docker-0.8.40.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"docker logs \"$container\"", "container-inspect.failure.json", "container.log", "--allow-private-ws", "root_my_custom_network", "landing.txt", "nip11.json", "unexpected NIP-11 relay name"} {
		if !strings.Contains(string(installer), required) {
			t.Fatalf("corrected installer does not retain failure evidence %q", required)
		}
	}
}
