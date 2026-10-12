package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestHanoverRecoveryProductionManifestIsExactPaidCity(t *testing.T) {
	data, err := os.ReadFile("deploy/production-additive-hanover-0.8.91.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest productionPromotionManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Cities) != 1 || manifest.Cities[0] != (productionPromotionCity{CityID: "d42123e1-1d01-473b-98e7-846342150eca", Slug: "hanover", Tier: "paid", RevisionID: "750f9dbf4e14cad5ac683772e318a46efe18e3a0fc5831d7eb021c21f92d69a3", ApprovalID: "7d53ad2bd7d50c390f2531bab25ffc1e07729e69272feb7d9709067f82bfb2b3"}) {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestHanoverRecoveryProductionInstallerIsAdditiveAndRollbackSafe(t *testing.T) {
	data, err := os.ReadFile("deploy/promote-hanover-production-0.8.91.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, value := range []string{"sha256sum -c", "production-additive-v1", "production-events.before.db", "production.candidate.db", "addedEventCount\")!=12", "Hanover signed IDs differ from review", "verify_audit", "london.candidate.json", "ws://127.0.0.1:3340", "registry.before.json", "entitlements.before.json", "production-journal.before.digest", "bitcoinwalk.org/hanover", "prior production database restored"} {
		if !strings.Contains(script, value) {
			t.Fatalf("missing guard %q", value)
		}
	}
	for _, value := range []string{"rm -rf", "RELAY_REPLICA_ENTITLEMENT_PLAN", "payment_invoice", "london.bitcoinwalk.org"} {
		if strings.Contains(script, value) {
			t.Fatalf("unexpected mutation %q", value)
		}
	}
}
