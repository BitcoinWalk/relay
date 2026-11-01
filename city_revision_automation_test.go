package main

import (
	"fiatjaf.com/nostr"
	"path/filepath"
	"testing"
)

func TestAutomatedCityRevisionIsContentOnlyAndManualModerationWins(t *testing.T) {
	admin, owner, automation := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, ownerPK, automationPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(owner), nostr.GetPublicKey(automation)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true, automationPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	policy.cityRevisionAutomation = &automationPK
	original := draftEvent(t, owner, cityA, 0)
	accept(t, relay, original)
	grant := cityGrant{CityID: cityA, CreatorPubkey: ownerPK.Hex(), CreatorRevisionID: original.ID.Hex(), EditorPubkeys: []string{ownerPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	first := cityDecision{CityID: cityA, RevisionID: original.ID.Hex(), Status: "approved"}
	accept(t, relay, workflowEvent(t, admin, 30304, first, nostr.Tags{{"d", cityA}, {"e", original.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2))
	content := map[string]any{"cityId": cityA, "slug": "test-city", "cityName": "Test City", "startAt": "2026-10-10T10:00:00Z", "description": "Updated walk", "meetingPoint": map[string]any{"description": "New square", "latitude": 1, "longitude": 2}, "chatUrl": "https://example.com/chat", "heroImageUrl": "https://example.com/image.jpg"}
	revision := workflowEvent(t, owner, 30303, content, nostr.Tags{{"d", cityA}, {"city", "test-city"}, {"e", original.ID.Hex(), "", "previous"}}, 3)
	accept(t, relay, revision)
	decision := cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved", Note: automatedCityRevisionNote}
	tags := nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}, {"client", "bitcoinwalk.org"}}
	accept(t, relay, workflowEvent(t, automation, 30304, decision, tags, 4))
	if policy.currentApproval(cityA).PubKey != automationPK {
		t.Fatal("automated content revision did not become current")
	}
	protected := content
	protected["slug"] = "other-city"
	bad := workflowEvent(t, owner, 30303, protected, nostr.Tags{{"d", cityA}, {"city", "other-city"}, {"e", revision.ID.Hex(), "", "previous"}}, 5)
	accept(t, relay, bad)
	badDecision := cityDecision{CityID: cityA, RevisionID: bad.ID.Hex(), Status: "approved", Note: automatedCityRevisionNote}
	badTags := nostr.Tags{{"d", cityA}, {"e", bad.ID.Hex(), "", "city-revision"}, {"status", "approved"}, {"client", "bitcoinwalk.org"}}
	deny(t, relay, workflowEvent(t, automation, 30304, badDecision, badTags, 6))
	manual := cityDecision{CityID: cityA, RevisionID: bad.ID.Hex(), Status: "rejected", Note: "Keep current URL"}
	accept(t, relay, workflowEvent(t, admin, 30304, manual, nostr.Tags{{"d", cityA}, {"e", bad.ID.Hex(), "", "city-revision"}, {"status", "rejected"}}, 7))
	deny(t, relay, workflowEvent(t, automation, 30304, badDecision, badTags, 8))
}
