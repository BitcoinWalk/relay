package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
)

func cityBrandEvent(t *testing.T, key nostr.SecretKey, cityID string, sequence uint64, previous, action, brand string, offset int) nostr.Event {
	t.Helper()
	binding := cityBrandBinding{Version: 1, CityID: cityID, Sequence: sequence, Previous: previous, Action: action, BrandPubkey: brand}
	content, _ := json.Marshal(binding)
	event := nostr.Event{Kind: cityBrandKind, CreatedAt: nostr.Now() + nostr.Timestamp(offset),
		Tags: nostr.Tags{{"d", fmt.Sprintf("%s:%d", cityID, sequence)}, {"i", cityID}, {"t", "bitcoinwalk-city-brand-v1"}}, Content: string(content)}
	if err := event.Sign(key); err != nil {
		t.Fatal(err)
	}
	return event
}

func approvedCityBrandRelay(t *testing.T) (*khatru.Relay, func(), nostr.SecretKey, nostr.SecretKey) {
	t.Helper()
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	proposal := draftEvent(t, creator, cityA, 10)
	accept(t, relay, proposal)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: proposal.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 11))
	decision := cityDecision{CityID: cityA, RevisionID: proposal.ID.Hex(), Status: "approved"}
	accept(t, relay, workflowEvent(t, admin, 30304, decision, nostr.Tags{{"d", cityA}, {"e", proposal.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 12))
	return relay, func() { relay.DisableExpirationManager(); db.Close() }, admin, creator
}

func TestCityBrandAdmissionRetainsExactHistory(t *testing.T) {
	relay, closeRelay, admin, _ := approvedCityBrandRelay(t)
	defer closeRelay()
	brand, replacement := nostr.GetPublicKey(nostr.Generate()).Hex(), nostr.GetPublicKey(nostr.Generate()).Hex()
	first := cityBrandEvent(t, admin, cityA, 0, "", "activate", brand, 20)
	accept(t, relay, first)
	accept(t, relay, first)
	second := cityBrandEvent(t, admin, cityA, 1, first.ID.Hex(), "replace", replacement, 21)
	accept(t, relay, second)
	revoke := cityBrandEvent(t, admin, cityA, 2, second.ID.Hex(), "revoke", replacement, 22)
	accept(t, relay, revoke)
	deny(t, relay, cityBrandEvent(t, admin, cityA, 3, revoke.ID.Hex(), "revoke", replacement, 23))
}

func TestCityBrandAdmissionRejectsMalformedStaleAndUnauthorizedRecords(t *testing.T) {
	relay, closeRelay, admin, creator := approvedCityBrandRelay(t)
	defer closeRelay()
	brand := nostr.GetPublicKey(nostr.Generate()).Hex()
	deny(t, relay, cityBrandEvent(t, creator, cityA, 0, "", "activate", brand, 20))
	deny(t, relay, cityBrandEvent(t, admin, cityB, 0, "", "activate", brand, 20))
	deny(t, relay, cityBrandEvent(t, admin, cityA, 0, "", "activate", nostr.GetPublicKey(creator).Hex(), 20))
	first := cityBrandEvent(t, admin, cityA, 0, "", "activate", brand, 20)
	accept(t, relay, first)
	deny(t, relay, cityBrandEvent(t, admin, cityA, 2, first.ID.Hex(), "replace", nostr.GetPublicKey(nostr.Generate()).Hex(), 21))
	malformed := cityBrandEvent(t, admin, cityA, 1, first.ID.Hex(), "replace", nostr.GetPublicKey(nostr.Generate()).Hex(), 21)
	malformed.Content = strings.TrimSuffix(malformed.Content, "}") + `,"owner":"private"}`
	malformed.Sign(admin)
	deny(t, relay, malformed)
}

func TestCityBrandRevocationSurvivesLostEligibility(t *testing.T) {
	relay, closeRelay, admin, _ := approvedCityBrandRelay(t)
	defer closeRelay()
	brand := nostr.GetPublicKey(nostr.Generate()).Hex()
	first := cityBrandEvent(t, admin, cityA, 0, "", "activate", brand, 20)
	accept(t, relay, first)
	moderation := eventModeration{CityID: cityA, Scope: "city", Target: cityA, Status: "suspended", Reason: "test"}
	accept(t, relay, workflowEvent(t, admin, eventModerationKind, moderation, nostr.Tags{{"d", cityA + ":00000000-0000-4000-8000-000000000099"}, {"i", cityA}, {"m", "city:" + cityA}, {"status", "suspended"}, {"client", "bitcoinwalk.org"}}, 21))
	deny(t, relay, cityBrandEvent(t, admin, cityA, 1, first.ID.Hex(), "replace", nostr.GetPublicKey(nostr.Generate()).Hex(), 22))
	accept(t, relay, cityBrandEvent(t, admin, cityA, 1, first.ID.Hex(), "revoke", brand, 23))
}
