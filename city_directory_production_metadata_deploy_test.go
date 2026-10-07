package main

import (
	"os"
	"strings"
	"testing"
)

func TestProductionDirectoryMetadataActivationPreservesSignedState(t *testing.T) {
	paths := []string{
		"deploy/90-directory-production.conf",
		"deploy/activate-directory-primary-production-metadata-0.8.83.sh",
		"deploy/compose.directory-2-production-0.8.83.yaml",
		"deploy/activate-directory-secondary-production-metadata-0.8.83.sh",
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
		"RELAY_CITY_DIRECTORY_TRANSPORT=production",
		"BitcoinWalk production directory relay",
		"memphis.before.json",
		"london.before.json",
		"memphis.after.json",
		"london.after.json",
		"cmp \"$backup/memphis.before.json\" \"$backup/memphis.after.json\"",
		"cmp \"$backup/london.before.json\" \"$backup/london.after.json\"",
		"read_only: true",
		"cap_drop: [ALL]",
		"no-new-privileges:true",
		"expose: [\"3343\"]",
	} {
		if !strings.Contains(combined, required) {
			t.Fatalf("production directory metadata deployment missing %q", required)
		}
	}
	for _, forbidden := range []string{"ports:", "privileged: true", "/var/run/docker.sock", "rm -rf"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("production directory metadata deployment contains unsafe operation %q", forbidden)
		}
	}
}
