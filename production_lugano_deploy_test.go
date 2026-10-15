package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLuganoProductionManifest(t *testing.T) {
	data, err := os.ReadFile("deploy/production-additive-lugano-0.9.5.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest productionPromotionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || len(manifest.Cities) != 1 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	city := manifest.Cities[0]
	if city.CityID != "7877ca26-dbe2-4dae-a7f7-483d94ce283a" || city.Slug != "lugano" || city.Tier != "free" || city.RevisionID != "0010e95112691d97cb62086eae1e4a77d4a3c15dfa99ec30f39b33655e9cf5d8" || city.ApprovalID != "45424b887a2e20da23bef6eb72aebaeb89330b1e1ca4518cde9205cfe5b4e767" {
		t.Fatalf("unexpected Lugano binding: %#v", city)
	}
}

func TestLuganoProductionInstallerContract(t *testing.T) {
	data, err := os.ReadFile("deploy/promote-lugano-production-0.9.5.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, value := range []string{"sha256sum -c", "production-additive-v1", "addedEventCount\")!=5", "Lugano signed IDs differ from review", "lugano.candidate.json", "london.candidate.json", "registry.before.json", "entitlements.before.json", "production-journal.before.digest", "bitcoinwalk.org/lugano", "bitcoinwalk-lugano", "prior production database restored"} {
		if !strings.Contains(script, value) {
			t.Fatalf("installer lacks %q", value)
		}
	}
}
