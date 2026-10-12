package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestGrazProductionManifestIsExactPaidCity(t *testing.T) {
	data, err := os.ReadFile("deploy/production-additive-graz-0.8.89.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest productionPromotionManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Cities) != 1 || manifest.Cities[0] != (productionPromotionCity{CityID: "e62592ac-a5fc-42df-8f31-94416f6ee8d5", Slug: "graz", Tier: "paid", RevisionID: "eb0e605fe77701f0771682271090b995d9e46836c63b87b0186285f8f66e6f9e", ApprovalID: "fff8a1d9bee7c0017aeeaaaab58d3f396fa8a66ce091e08316da869ad4b5a010"}) {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestGrazProductionInstallerIsAdditiveAndRollbackSafe(t *testing.T) {
	data, err := os.ReadFile("deploy/promote-graz-production-0.8.89.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, value := range []string{"sha256sum -c", "production-additive-v1", "production-events.before.db", "production.candidate.db", "addedEventCount\")!=5", "Graz signed IDs differ from review", "verify_audit", "london.after.json", "registry.before.json", "entitlements.before.json", "production-journal.before.digest", "bitcoinwalk.org/graz", "prior production database restored"} {
		if !strings.Contains(script, value) {
			t.Fatalf("missing guard %q", value)
		}
	}
	for _, value := range []string{"rm -rf", "RELAY_REPLICA_ENTITLEMENT_PLAN", "payment_invoice"} {
		if strings.Contains(script, value) {
			t.Fatalf("unexpected mutation %q", value)
		}
	}
}
