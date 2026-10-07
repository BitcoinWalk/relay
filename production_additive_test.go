package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
)

func TestProductionAdditivePreservesBaseAndAddsOnlySelectedCities(t *testing.T) {
	dir := t.TempDir()
	admin, existingOwner, selectedOwner, unrelatedOwner := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	existing := promotionCityEvents(t, admin, existingOwner, cityA, "existing", 0)
	selected := promotionCityEvents(t, admin, selectedOwner, cityB, "selected", 20)
	unrelated := promotionCityEvents(t, admin, unrelatedOwner, "84c7c7f2-b37b-45ca-82ec-4f283407e5c9", "unrelated", 40)
	productionOnly := workflowEvent(t, admin, 30308, map[string]any{"feature": true}, nostr.Tags{{"d", "production-only"}}, 60)
	source, base, destination, manifest := filepath.Join(dir, "source.db"), filepath.Join(dir, "base.db"), filepath.Join(dir, "candidate.db"), filepath.Join(dir, "manifest.json")
	savePromotionEvents(t, source, append(append(selected, unrelated...), productionOnly))
	savePromotionEvents(t, base, append(existing, productionOnly))
	writePromotionManifest(t, manifest, productionPromotionCity{CityID: cityB, Slug: "selected", Tier: "free", RevisionID: selected[0].ID.Hex(), ApprovalID: selected[2].ID.Hex()})

	result, err := buildProductionAdditiveWithAdmin(source, base, destination, manifest, productionAdditiveConfirmation, nostr.GetPublicKey(admin))
	if err != nil {
		t.Fatal(err)
	}
	if result.BaseEventCount != 5 || result.AddedEventCount != 4 || result.FinalEventCount != 9 || len(result.Cities) != 1 || result.Cities[0] != cityB || result.FinalEventDigest == "" || result.AddedEventDigest == "" {
		t.Fatalf("unexpected additive result: %#v", result)
	}
	db := &boltdb.BoltBackend{Path: destination}
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	found := map[string]bool{}
	for event := range db.QueryEvents(nostr.Filter{}, 30) {
		found[event.ID.Hex()] = true
		if promotionEventCity(event, map[string]bool{"84c7c7f2-b37b-45ca-82ec-4f283407e5c9": true}) {
			t.Fatalf("unrelated city leaked: %s", event.ID.Hex())
		}
	}
	for _, event := range append(existing, append(selected, productionOnly)...) {
		if !found[event.ID.Hex()] {
			t.Fatalf("expected event missing: %s", event.ID.Hex())
		}
	}
}

func TestProductionAdditiveFailsClosed(t *testing.T) {
	dir := t.TempDir()
	admin, owner := nostr.Generate(), nostr.Generate()
	events := promotionCityEvents(t, admin, owner, cityA, "selected", 0)
	source, base, manifest := filepath.Join(dir, "source.db"), filepath.Join(dir, "base.db"), filepath.Join(dir, "manifest.json")
	savePromotionEvents(t, source, events)
	savePromotionEvents(t, base, events[:1])
	writePromotionManifest(t, manifest, productionPromotionCity{CityID: cityA, Slug: "selected", Tier: "free", RevisionID: events[0].ID.Hex(), ApprovalID: events[2].ID.Hex()})
	if _, err := buildProductionAdditiveWithAdmin(source, base, filepath.Join(dir, "weak.db"), manifest, "yes", nostr.GetPublicKey(admin)); err == nil {
		t.Fatal("weak confirmation accepted")
	}
	wrongApproval := "f" + events[2].ID.Hex()[1:]
	if wrongApproval == events[2].ID.Hex() {
		wrongApproval = "e" + events[2].ID.Hex()[1:]
	}
	bad := productionPromotionManifest{Version: 1, Cities: []productionPromotionCity{{CityID: cityA, Slug: "selected", Tier: "free", RevisionID: events[0].ID.Hex(), ApprovalID: wrongApproval}}}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildProductionAdditiveWithAdmin(source, base, filepath.Join(dir, "stale.db"), manifest, productionAdditiveConfirmation, nostr.GetPublicKey(admin)); err == nil {
		t.Fatal("stale approval accepted")
	}
}
