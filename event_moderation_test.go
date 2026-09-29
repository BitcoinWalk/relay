package main

import (
	"encoding/json"
	"fiatjaf.com/nostr"
	"fmt"
	"path/filepath"
	"testing"
)

func moderationEvent(t *testing.T, key nostr.SecretKey, m eventModeration, offset int) nostr.Event {
	return workflowEvent(t, key, eventModerationKind, m, nostr.Tags{{"d", m.CityID + ":00000000-0000-4000-8000-" + fmtModerationOffset(offset)}, {"i", m.CityID}, {"m", moderationKey(m)}, {"status", m.Status}, {"client", "bitcoinwalk.org"}}, offset)
}
func fmtModerationOffset(n int) string { return fmt.Sprintf("%012d", n) }

func TestEventModerationLifecycle(t *testing.T) {
	admin, creator, other := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	path := filepath.Join(t.TempDir(), "moderation.db")
	relay, db, err := newRelay(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	p := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	revision := draftEvent(t, creator, cityA, 0)
	accept(t, relay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, relay, approval)
	first := replicatedOccurrence(t, creator, revision, approval, "2026-10-10", 3)
	accept(t, relay, first)
	second := replicatedOccurrence(t, creator, revision, approval, "2026-10-11", 4)
	accept(t, relay, second)
	m := eventModeration{CityID: cityA, Scope: "event", Target: calendarAddress(first), EventID: first.ID.Hex(), Status: "hidden", Reason: "Test moderation"}
	p.replicaJournal = &replicaJournal{registry: &replicaRegistry{destinations: map[string]string{cityA: "wss://replica.example/"}}}
	deny(t, relay, moderationEvent(t, admin, m, 5))
	p.replicaJournal = nil
	deny(t, relay, moderationEvent(t, other, m, 5))
	cross := m
	cross.CityID = cityB
	deny(t, relay, moderationEvent(t, admin, cross, 5))
	hidden := moderationEvent(t, admin, m, 5)
	accept(t, relay, hidden)
	if _, err := p.exportReplicaBundle(cityA, second.ID.Hex()); err == nil {
		t.Fatal("moderated city exported without receiver support")
	}
	if _, err := p.recoverReplicaJournal(&replicaJournal{registry: &replicaRegistry{destinations: map[string]string{cityA: "wss://replica.example/"}}}); err == nil {
		t.Fatal("moderated city promoted into replication")
	}
	if p.checkCalendarRead(first) == nil || p.checkCalendarRead(second) != nil {
		t.Fatal("hide did not isolate target")
	}
	for range relay.QueryStored(t.Context(), nostr.Filter{IDs: []nostr.ID{first.ID}}) {
		t.Fatal("hidden exact ID leaked")
	}
	if !relay.PreventBroadcast(nil, nostr.Filter{}, first) {
		t.Fatal("hidden broadcast allowed")
	}
	edit := replicatedOccurrence(t, creator, revision, approval, "2026-10-10", 6)
	edit.Content = "Hidden edit"
	edit.Sign(creator)
	deny(t, relay, edit)
	stale := m
	stale.Status = "visible"
	deny(t, relay, moderationEvent(t, admin, stale, 7))
	stale.Previous = hidden.ID.Hex()
	visible := moderationEvent(t, admin, stale, 7)
	accept(t, relay, visible)
	accept(t, relay, hidden) // old exact retry must not override the new head
	if p.checkCalendarRead(first) != nil {
		t.Fatal("unhide failed")
	}
	for i, scope := range []string{"author", "city"} {
		target := creatorPK.Hex()
		if scope == "city" {
			target = cityA
		}
		suspend := eventModeration{CityID: cityA, Scope: scope, Target: target, Status: "suspended", Reason: "Pause publication"}
		decision := moderationEvent(t, admin, suspend, 10+i*3)
		accept(t, relay, decision)
		if p.checkCalendarRead(first) != nil {
			t.Fatal("suspension hid existing history")
		}
		newEvent := replicatedOccurrence(t, creator, revision, approval, "2026-10-12", 11+i*3)
		deny(t, relay, newEvent)
		suspend.Status = "active"
		suspend.Previous = decision.ID.Hex()
		accept(t, relay, moderationEvent(t, admin, suspend, 12+i*3))
		accept(t, relay, newEvent)
	}
	// Cancellation is permanent even after a later visibility decision.
	deletion := workflowEvent(t, creator, 5, "cancel", nostr.Tags{{"e", first.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, 18)
	accept(t, relay, deletion)
	again := m
	again.Previous = visible.ID.Hex()
	hiddenAgain := moderationEvent(t, admin, again, 19)
	accept(t, relay, hiddenAgain)
	again.Status = "visible"
	again.Previous = hiddenAgain.ID.Hex()
	accept(t, relay, moderationEvent(t, admin, again, 20))
	if p.checkCalendarRead(first) == nil {
		t.Fatal("unhide resurrected cancellation")
	}
	var got cityGrant
	_, grantEvent, _ := p.grant(cityA)
	json.Unmarshal([]byte(grantEvent.Content), &got)
	if got.CreatorPubkey != creatorPK.Hex() {
		t.Fatal("ownership changed")
	}
	// Retained decisions survive reopening storage.
	hideSecond := m
	hideSecond.Target = calendarAddress(second)
	hideSecond.EventID = second.ID.Hex()
	accept(t, relay, moderationEvent(t, admin, hideSecond, 21))
	relay.DisableExpirationManager()
	db.Close()
	relay, db, err = newRelay(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	p = unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	if p.checkCalendarRead(second) == nil {
		t.Fatal("restart lost hide")
	}
	if p.byID(first.ID.Hex()) == nil || p.byID(hidden.ID.Hex()) == nil {
		t.Fatal("history removed")
	}
}
