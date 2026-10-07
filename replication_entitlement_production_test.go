package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestProductionEntitlementIssueAndFirstCityBootstrap(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "authority-key")
	if err := createRelayCredential(keyPath); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(dir, "entitlements.json")
	evidence := strings.Repeat("a", 64)
	now := time.Unix(1800000000, 0)
	issued, err := issueProductionReplicaEntitlement(replicaProductionEntitlementIssueInput{AuthorityKeyPath: keyPath, LedgerPath: ledgerPath, CityID: cityB, Status: replicaEntitlementActive, EvidenceDigest: evidence, Confirmation: replicaEntitlementProductionIssueConfirm, Now: now})
	if err != nil || issued.CityID != cityB || issued.Status != replicaEntitlementActive || issued.EvidenceDigest != evidence || issued.EntitlementEventID == "" {
		t.Fatalf("production entitlement issue failed: %+v %v", issued, err)
	}
	key, err := loadReplicaServiceKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	authority := nostr.GetPublicKey(key)
	ledger, err := loadReplicaEntitlementLedger(ledgerPath, authority, now)
	if err != nil {
		t.Fatal(err)
	}
	record, active := ledger.active(cityB)
	if !active || record.Event.ID.Hex() != issued.EntitlementEventID || record.Content.EvidenceDigest != evidence {
		t.Fatal("issued production entitlement is not current and exact")
	}
	if basis, err := uniqueTag(record.Event, "basis"); err != nil || basis != replicaEntitlementProductionBasis {
		t.Fatal("production entitlement lacks its payment basis")
	}

	registryPath := filepath.Join(dir, "registry.json")
	journalPath := filepath.Join(dir, "journal.db")
	result, err := bootstrapEntitledReplicaCity(replicaEntitlementBootstrapInput{RegistryPath: registryPath, JournalPath: journalPath, LedgerPath: ledgerPath, Authority: authority, CityID: cityB, Destination: "wss://london.example/", Confirmation: replicaEntitlementBootstrapConfirm, Now: now})
	if err != nil || result.CityID != cityB || result.EntitlementEventID != issued.EntitlementEventID || result.RegistryDigest == "" {
		t.Fatalf("production registry bootstrap failed: %+v %v", result, err)
	}
	registry, err := loadReplicaRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateEntitledReplicaRegistry(registry, ledger); err != nil {
		t.Fatal(err)
	}
	if destination, ok := registry.destination(cityB); !ok || destination != "wss://london.example/" {
		t.Fatal("production registry has the wrong first destination")
	}
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatal("production journal was not initialized")
	}
}

func TestProductionEntitlementIssueFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "authority-key")
	if err := createRelayCredential(keyPath); err != nil {
		t.Fatal(err)
	}
	base := replicaProductionEntitlementIssueInput{AuthorityKeyPath: keyPath, LedgerPath: filepath.Join(dir, "ledger.json"), CityID: cityB, Status: replicaEntitlementActive, EvidenceDigest: strings.Repeat("b", 64), Confirmation: replicaEntitlementProductionIssueConfirm}
	wrongEvidence := base
	wrongEvidence.EvidenceDigest = "not-a-digest"
	if _, err := issueProductionReplicaEntitlement(wrongEvidence); err == nil {
		t.Fatal("production entitlement accepted invalid payment evidence")
	}
	wrongConfirmation := base
	wrongConfirmation.Confirmation = "wrong"
	if _, err := issueProductionReplicaEntitlement(wrongConfirmation); err == nil {
		t.Fatal("production entitlement accepted the wrong confirmation")
	}
	if _, err := os.Stat(base.LedgerPath); !os.IsNotExist(err) {
		t.Fatal("failed production issue left a ledger")
	}
}

func TestProductionEntitlementLedgerDoesNotContainPaymentHash(t *testing.T) {
	data, err := os.ReadFile("replication_entitlement_production.go")
	if err != nil {
		t.Fatal(err)
	}
	var event replicaEntitlementContent
	if json.Unmarshal([]byte(`{"version":1,"cityId":"`+cityB+`","plan":"bitcoinwalk-paid-city-lifetime-v1","status":"active","evidenceDigest":"`+strings.Repeat("c", 64)+`"}`), &event) != nil {
		t.Fatal("fixture failed")
	}
	if strings.Contains(string(data), "PaymentHash") || strings.Contains(string(data), "paymentHash") {
		t.Fatal("production entitlement code stores a raw payment hash")
	}
}
