package main

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestAutomatedNewCityRequiresExactBasicSubmissionAndWalks(t *testing.T) {
	admin, creator, automation := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, creatorPK, automationPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator), nostr.GetPublicKey(automation)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{automationPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	policy.newCityAutomation = &automationPK
	start := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	walk := nostr.Event{Kind: 31923, CreatedAt: nostr.Now(), Content: "Test walk", Tags: nostr.Tags{
		{"d", cityA + ":" + start.Format("2006-01-02")}, {"title", "BitcoinWalk Test City"}, {"summary", "BitcoinWalk in Test City"}, {"image", "https://example.com/image.jpg"},
		{"start", strconv.FormatInt(start.Unix(), 10)}, {"D", strconv.FormatInt(start.Unix()/86400, 10)}, {"location", "Square"}, {"location", "1,2"}, {"g", encodeGeohash(1, 2)}, {"t", "bitcoinwalk"}, {"r", "https://example.com/chat"},
		{"end", strconv.FormatInt(start.Add(time.Hour).Unix(), 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "initial-proposal-v1"},
	}}
	walk.Sign(creator)
	accept(t, relay, walk)
	revision := draftEvent(t, creator, cityA, 1)
	var city map[string]any
	if json.Unmarshal([]byte(revision.Content), &city) != nil {
		t.Fatal("draft JSON")
	}
	city["startAt"] = start.Format(time.RFC3339)
	city["requestedTier"] = "free"
	revision.Content = string(mustJSONBytes(city))
	revision.Tags = append(revision.Tags, nostr.Tag{"e", walk.ID.Hex(), "", "initial-walk"})
	revision.Sign(creator)
	accept(t, relay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	grantEvent := workflowEvent(t, automation, 30302, grant, nostr.Tags{{"d", cityA}, {"client", "bitcoinwalk.org"}, {"automation", automatedNewCityMarker}}, 2)
	accept(t, relay, grantEvent)
	decision := cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), InitialEventIDs: []string{walk.ID.Hex()}, Status: "approved", Note: automatedNewCityNote}
	approval := workflowEvent(t, automation, 30304, decision, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"e", walk.ID.Hex(), "", "initial-walk"}, {"status", "approved"}, {"client", "bitcoinwalk.org"}}, 3)
	accept(t, relay, approval)
	if policy.currentApproval(cityA).ID != approval.ID {
		t.Fatal("automated new city approval did not become current")
	}
	broadened := grant
	broadened.EditorPubkeys = append(broadened.EditorPubkeys, adminPK.Hex())
	deny(t, relay, workflowEvent(t, automation, 30302, broadened, nostr.Tags{{"d", cityB}, {"client", "bitcoinwalk.org"}, {"automation", automatedNewCityMarker}}, 4))
}

func mustJSONBytes(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
