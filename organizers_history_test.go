package main

import (
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
)

func TestOrganizerRetainedHistory(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	path := filepath.Join(t.TempDir(), "events.db")
	relay, db, err := newRelay(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	// A pre-upgrade city and approval must survive new-format edits.
	original := draftEvent(t, creator, cityA, 0)
	original.Tags = nostr.Tags{{"d", cityA}, {"city", "test-city"}}
	original.Sign(creator)
	accept(t, relay, original)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: original.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	decision := cityDecision{CityID: cityA, RevisionID: original.ID.Hex(), Status: "approved"}
	approval := workflowEvent(t, admin, 30304, decision, nostr.Tags{{"d", cityA}, {"e", original.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	approval.Tags[0] = nostr.Tag{"d", cityA}
	approval.Sign(admin)
	accept(t, relay, approval)
	edit := draftEvent(t, creator, cityA, 3)
	accept(t, relay, edit)
	// Reject this edit; the prior approval is retained, not replaced.
	reject := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: edit.ID.Hex(), Status: "rejected"}, nostr.Tags{{"d", cityA}, {"e", edit.ID.Hex(), "", "city-revision"}, {"status", "rejected"}}, 4)
	accept(t, relay, reject)
	accept(t, relay, approval) // signed-event retry must not replace newer decisions
	for _, e := range []nostr.Event{original, approval, edit, reject} {
		if policy.byID(e.ID.Hex()) == nil {
			t.Fatalf("lost history %s", e.ID.Hex())
		}
	}
	// A signed replacement, including a legacy address, cannot destroy history.
	for _, e := range []nostr.Event{original, edit} {
		e.CreatedAt += 10
		e.Sign(creator)
		deny(t, relay, e)
	}
	for _, e := range []nostr.Event{approval, reject} {
		e.CreatedAt += 10
		e.Sign(admin)
		deny(t, relay, e)
	}
	// A fresh address cannot bypass decision chronology.
	stale := workflowEvent(t, admin, 30304, decision, nostr.Tags{{"d", cityA}, {"e", original.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 3)
	deny(t, relay, stale)
	relay.DisableExpirationManager()
	db.Close()
	relay, db, err = newRelay(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy = unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	for _, e := range []nostr.Event{original, approval, edit, reject} {
		if policy.byID(e.ID.Hex()) == nil {
			t.Fatal("history lost after restart")
		}
	}
	if policy.latestDecision(cityA).ID != reject.ID {
		t.Fatal("wrong latest decision after restart")
	}
	// Creator registration still references a retained original after later edits.
	grant.EditorPubkeys = append(grant.EditorPubkeys, nostr.GetPublicKey(nostr.Generate()).Hex())
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 5))
}
