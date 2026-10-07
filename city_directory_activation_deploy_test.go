package main

import (
	"os"
	"strings"
	"testing"
)

func TestDirectoryActivationUpgradePreservesRootsAndIsolation(t *testing.T) {
	paths := []string{
		"deploy/upgrade-directory-primary-activation-0.8.84.sh",
		"deploy/upgrade-directory-secondary-activation-0.8.84.sh",
		"deploy/compose.directory-2-production-0.8.84.yaml",
	}
	combined := ""
	for _, path := range paths {
		value, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		combined += string(value)
	}
	for _, required := range []string{
		"bitcoinwalk-directory-transport-0.8.84",
		"memphis.before.json",
		"london.before.json",
		"memphis.after-restart.json",
		"london.after-restart.json",
		"read_only: true",
		"user: \"65532:65532\"",
		"cap_drop: [ALL]",
		"no-new-privileges:true",
		"expose: [\"3343\"]",
	} {
		if !strings.Contains(combined, required) {
			t.Fatalf("directory activation deployment missing %q", required)
		}
	}
	for _, forbidden := range []string{"ports:", "network_mode: host", "privileged: true", "/var/run/docker.sock", "rm -rf"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("directory activation deployment contains unsafe operation %q", forbidden)
		}
	}
}
