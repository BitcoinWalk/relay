package main

import (
	"os"
	"strings"
	"testing"
)

func TestCityDirectoryTransportDeploymentIsIsolatedAndBackupFirst(t *testing.T) {
	unit, err := os.ReadFile("deploy/bitcoinwalk-directory-staging.service")
	if err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile("deploy/install-directory-transport-0.8.35.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"DynamicUser=yes",
		"StateDirectory=bitcoinwalk-directory-staging",
		"RELAY_LISTEN=127.0.0.1:3343",
		"RELAY_CITY_DIRECTORY_TRANSPORT=staging",
		"RELAY_CITY_DIRECTORY_TRANSPORT_ANCHORS=/etc/bitcoinwalk-directory/anchors.json",
		"RELAY_CITY_DIRECTORY_TRANSPORT_BUNDLE=/etc/bitcoinwalk-directory/memphis-bundle.json",
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
	} {
		if !strings.Contains(string(unit), required) {
			t.Fatalf("directory unit missing %q", required)
		}
	}
	for _, required := range []string{
		"mktemp -d /var/backups/bitcoinwalk-directory-staging.",
		"cp -p \"$database\" \"$backup/events.db\"",
		"systemctl is-active --quiet \"$service\"",
		"http://127.0.0.1:3343/healthz",
		"ws://127.0.0.1:3343/",
		"No Caddy or DNS configuration was changed.",
	} {
		if !strings.Contains(string(installer), required) {
			t.Fatalf("directory installer missing %q", required)
		}
	}
	for _, forbidden := range []string{"/etc/caddy/Caddyfile", "systemctl reload caddy", "directory-staging.bitcoinwalk.org {"} {
		if strings.Contains(string(installer), forbidden) {
			t.Fatalf("loopback-only installer contains premature public activation %q", forbidden)
		}
	}
}
