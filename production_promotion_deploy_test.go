package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestProductionPromotionManifestHasOnlyLondonPaid(t *testing.T) {
	data, err := os.ReadFile("deploy/production-promotion-0.8.64.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest productionPromotionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || len(manifest.Cities) != 12 {
		t.Fatalf("unexpected manifest size: %#v", manifest)
	}
	paid := []productionPromotionCity{}
	for _, city := range manifest.Cities {
		if city.Tier == "paid" {
			paid = append(paid, city)
		}
	}
	if len(paid) != 1 || paid[0].CityID != "ca2f9905-fb4d-4948-a12c-c792b28ec7c8" || paid[0].Slug != "london" {
		t.Fatalf("unexpected paid promotion: %#v", paid)
	}
}

func TestProductionPromotionRehearsalCannotInstallCandidate(t *testing.T) {
	data, err := os.ReadFile("deploy/rehearse-production-promotion-0.8.64.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"sha256sum -c", "production-live.db", "production.candidate.db", "production-promotion-v1", "-allow-empty", "byte-identical"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing rehearsal guard %q", required)
		}
	}
	for _, forbidden := range []string{"install -m", "cp -p \"$candidate\" \"$production_db\"", "systemctl reload caddy", "resolvectl"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("rehearsal contains live activation %q", forbidden)
		}
	}
}

func TestProductionPromotionAcceptanceIsReadOnly(t *testing.T) {
	data, err := os.ReadFile("deploy/complete-production-promotion-rehearsal-0.8.65.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"London is not the sole paid city", "cmp /var/lib/bitcoinwalk-relay-production/events.db", "app-health.accepted.json", "SHA256SUMS.accepted"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing acceptance guard %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl stop", "systemctl restart", "docker", "install -m", "cp -p"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("acceptance script contains mutation %q", forbidden)
		}
	}
}
