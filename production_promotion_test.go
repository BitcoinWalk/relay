package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
)

func promotionCityEvents(t *testing.T, admin, creator nostr.SecretKey, cityID, slug string, offset int) []nostr.Event {
	t.Helper()
	draft := workflowEvent(t, creator, 30303, map[string]any{
		"cityId": cityID, "slug": slug, "cityName": slug, "startAt": "2026-10-03T10:00:00Z", "description": "Production promotion test",
		"meetingPoint": map[string]any{"description": "Square", "latitude": 1, "longitude": 2}, "chatUrl": "https://example.com/chat", "requestedTier": "paid",
	}, nostr.Tags{{"d", cityID}, {"city", slug}}, offset)
	creatorPK := nostr.GetPublicKey(creator).Hex()
	grant := workflowEvent(t, admin, 30302, cityGrant{CityID: cityID, EditorPubkeys: []string{creatorPK}, SuperAdminPubkey: nostr.GetPublicKey(admin).Hex(), CreatorPubkey: creatorPK, CreatorRevisionID: draft.ID.Hex()}, nostr.Tags{{"d", cityID}}, offset+1)
	decision := cityDecision{CityID: cityID, RevisionID: draft.ID.Hex(), Slug: slug, Status: "approved", Note: "Promote"}
	approval := workflowEvent(t, admin, 30304, decision, nostr.Tags{{"d", cityID}, {"status", "approved"}, {"city", slug}, {"e", draft.ID.Hex(), "", "city-revision"}}, offset+2)
	walk := workflowEvent(t, creator, 31923, "", nostr.Tags{{"d", "walk-" + slug}, {"i", cityID}, {"title", "BitcoinWalk " + slug}, {"start", "1791021600"}, {"start_tzid", "UTC"}, {"location", "Square"}, {"g", "s00"}, {"bitcoinwalk", "occurrence-v1"}, {"e", draft.ID.Hex(), "", "city-revision"}, {"e", approval.ID.Hex(), "", "city-approval"}}, offset+3)
	return []nostr.Event{draft, grant, approval, walk}
}

func savePromotionEvents(t *testing.T, path string, events []nostr.Event) {
	t.Helper()
	db := &boltdb.BoltBackend{Path: path}
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if err := db.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
}

func writePromotionManifest(t *testing.T, path string, city productionPromotionCity) {
	t.Helper()
	data, err := json.Marshal(productionPromotionManifest{Version: 1, Cities: []productionPromotionCity{city}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPromotionSelectsApprovedCitiesAndGlobalPolicy(t *testing.T) {
	dir := t.TempDir()
	admin, creator, outsider := nostr.Generate(), nostr.Generate(), nostr.Generate()
	selected := promotionCityEvents(t, admin, creator, cityA, "london", 0)
	unrelated := promotionCityEvents(t, admin, outsider, cityB, "unrelated", 20)
	page := contentEvent(t, admin, testPage("88f137cb-2ac1-4eef-8358-7dd66b45922f", "about"), 40)
	source, destination, manifest := filepath.Join(dir, "source.db"), filepath.Join(dir, "production.db"), filepath.Join(dir, "manifest.json")
	savePromotionEvents(t, source, append(append(selected, unrelated...), page))
	writePromotionManifest(t, manifest, productionPromotionCity{CityID: cityA, Slug: "london", Tier: "paid", RevisionID: selected[0].ID.Hex(), ApprovalID: selected[2].ID.Hex()})
	result, err := buildProductionPromotionWithAdmin(source, destination, manifest, productionPromotionConfirmation, nostr.GetPublicKey(admin))
	if err != nil {
		t.Fatal(err)
	}
	if result.EventCount != 5 || len(result.PaidCities) != 1 || result.PaidCities[0] != cityA || len(result.FreeCities) != 0 || result.EventDigest == "" || result.ManifestDigest == "" {
		t.Fatalf("unexpected promotion: %#v", result)
	}
	db := &boltdb.BoltBackend{Path: destination}
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for event := range db.QueryEvents(nostr.Filter{}, 20) {
		if promotionEventCity(event, map[string]bool{cityB: true}) {
			t.Fatalf("unrelated city leaked: %s", event.ID.Hex())
		}
	}
}

func TestProductionPromotionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	admin, creator := nostr.Generate(), nostr.Generate()
	events := promotionCityEvents(t, admin, creator, cityA, "london", 0)
	source, manifest := filepath.Join(dir, "source.db"), filepath.Join(dir, "manifest.json")
	savePromotionEvents(t, source, events)
	writePromotionManifest(t, manifest, productionPromotionCity{CityID: cityA, Slug: "london", Tier: "paid", RevisionID: events[0].ID.Hex(), ApprovalID: events[2].ID.Hex()})
	if _, err := buildProductionPromotionWithAdmin(source, filepath.Join(dir, "wrong-confirmation.db"), manifest, "yes", nostr.GetPublicKey(admin)); err == nil {
		t.Fatal("weak confirmation accepted")
	}
	bad := productionPromotionManifest{Version: 1, Cities: []productionPromotionCity{{CityID: cityA, Slug: "london", Tier: "paid", RevisionID: events[0].ID.Hex(), ApprovalID: "f" + events[2].ID.Hex()[1:]}}}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildProductionPromotionWithAdmin(source, filepath.Join(dir, "stale.db"), manifest, productionPromotionConfirmation, nostr.GetPublicKey(admin)); err == nil {
		t.Fatal("stale approval accepted")
	}
}
