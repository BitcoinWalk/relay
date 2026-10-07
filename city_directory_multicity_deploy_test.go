package main

import (
	"os"
	"strings"
	"testing"
)

func TestMultiCityDirectoryUpgradePackagesPreserveIsolationAndRoots(t *testing.T) {
	primary, err := os.ReadFile("deploy/upgrade-directory-primary-multicity-0.8.79.sh")
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := os.ReadFile("deploy/upgrade-directory-secondary-multicity-0.8.79.sh")
	if err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile("deploy/bitcoinwalk-directory-staging-0.8.79.service")
	if err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile("deploy/compose.directory-2-staging-0.8.79.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"systemctl stop \"$service\"", "cp -p \"$database\" \"$backup/events.db\"", "public.before.json", "loopback.after-restart.json", "bitcoinwalk-directory-transport-0.8.79"} {
		if !strings.Contains(string(primary), required) {
			t.Fatalf("primary upgrade missing %q", required)
		}
	}
	for _, required := range []string{"docker stop -t 20", "cp -p \"$database\" \"$backup/events.db\"", "public.before.json", "private.after-restart.json", "ReadonlyRootfs", "test -z \"$(docker port"} {
		if !strings.Contains(string(secondary), required) {
			t.Fatalf("secondary upgrade missing %q", required)
		}
	}
	if strings.Contains(string(unit), "RELAY_CITY_DIRECTORY_TRANSPORT_CITY") || strings.Contains(string(compose), "RELAY_CITY_DIRECTORY_TRANSPORT_CITY") {
		t.Fatal("multi-city runtime remains pinned to one city")
	}
	for _, required := range []string{"DynamicUser=yes", "ProtectSystem=strict", "RELAY_LISTEN=127.0.0.1:3343"} {
		if !strings.Contains(string(unit), required) {
			t.Fatalf("primary unit missing %q", required)
		}
	}
	for _, required := range []string{"read_only: true", "user: \"65532:65532\"", "cap_drop:", "expose:", "name: root_my_custom_network"} {
		if !strings.Contains(string(compose), required) {
			t.Fatalf("secondary compose missing %q", required)
		}
	}
	for _, forbidden := range []string{"ports:", "network_mode: host", "privileged: true", "/var/run/docker.sock"} {
		if strings.Contains(string(compose), forbidden) {
			t.Fatalf("secondary compose contains %q", forbidden)
		}
	}
}
