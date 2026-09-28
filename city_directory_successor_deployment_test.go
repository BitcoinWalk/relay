package main

import (
	"os"
	"strings"
	"testing"
)

func TestCityDirectorySuccessorRehearsalIsBackupFirstAndNonInstalling(t *testing.T) {
	data, err := os.ReadFile("deploy/rehearse-city-directory-successors-0.8.42.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		`mktemp -d /var/backups/bitcoinwalk-city-directory-successors.`,
		`cp -p "$anchor" "$backup/anchors.json"`,
		`cp -p "$bundle" "$backup/memphis-bundle.json"`,
		`RELAY_CITY_DIRECTORY_SUCCESSOR_REHEARSAL=isolated-successors-v1`,
		`cmp "$backup/primary-before.json" "$backup/primary-after.json"`,
		`cmp "$backup/secondary-before.json" "$backup/secondary-after.json"`,
		`No rehearsal event or executable was installed into a live path.`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("rehearsal omitted safety invariant %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl stop", "systemctl restart", "docker restart", "install -m", "cp -p \"$artifact\""} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("rehearsal mutates live state through %q", forbidden)
		}
	}
}
