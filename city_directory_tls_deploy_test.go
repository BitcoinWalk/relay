package main

import (
	"os"
	"strings"
	"testing"
)

func TestCityDirectoryPublicTLSDeploymentIsCaddyOnlyAndRollbackSafe(t *testing.T) {
	fragment, err := os.ReadFile("deploy/Caddyfile.directory-staging")
	if err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile("deploy/activate-directory-tls-0.8.36.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"DIRECTORY_STAGING_HOST {", "reverse_proxy 127.0.0.1:3343", "X-Forwarded-Host DIRECTORY_STAGING_HOST", "X-Forwarded-Proto https"} {
		if !strings.Contains(string(fragment), required) {
			t.Fatalf("directory Caddy fragment missing %q", required)
		}
	}
	for _, required := range []string{
		"4c994fa4c1d603bb9eb22d9ee1c3720812887ac562a3a51c30b14477033eeb2d",
		"getent ahostsv4 \"$host\"",
		"mktemp -d /var/backups/bitcoinwalk-directory-tls.",
		"cp -p /etc/caddy/Caddyfile \"$backup/Caddyfile\"",
		"caddy validate --config \"$backup/Caddyfile.candidate\"",
		"install -o root -g root -m 0644 \"$backup/Caddyfile\" /etc/caddy/Caddyfile",
		"systemctl reload caddy.service",
		"https://$host/healthz",
		"wss://$host/",
		"cmp \"$backup/audit-loopback.json\" \"$backup/audit-public.json\"",
	} {
		if !strings.Contains(string(installer), required) {
			t.Fatalf("directory TLS installer missing %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl restart bitcoinwalk-directory-staging", "RELAY_DB=", "/var/lib/bitcoinwalk-directory-staging/events.db"} {
		if strings.Contains(string(installer), forbidden) {
			t.Fatalf("Caddy-only activation contains service/data mutation %q", forbidden)
		}
	}
}
