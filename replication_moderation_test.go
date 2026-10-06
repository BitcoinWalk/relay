package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestReplicaReceiverCarriesOrderedModerationWithoutResurrection(t *testing.T) {
	admin, creator, service := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	destination := "wss://madeira.example/"

	sourceRelay, sourceDB, err := newRelay(filepath.Join(t.TempDir(), "source.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	defer sourceRelay.DisableExpirationManager()
	source := unitOrganizerPolicy(sourceRelay, enableOrganizers(sourceRelay, sourceDB, adminPK))
	revision := draftEvent(t, creator, cityA, 0)
	accept(t, sourceRelay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, sourceRelay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, sourceRelay, approval)
	occurrence := replicatedOccurrence(t, creator, revision, approval, "2026-10-10", 3)
	accept(t, sourceRelay, occurrence)

	registry := &replicaRegistry{destinations: map[string]string{cityA: destination}, entitlements: map[string]string{}}
	journal, err := openReplicaJournal(filepath.Join(t.TempDir(), "journal.db"), registry, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if recovered, err := source.recoverReplicaJournal(journal); err != nil || recovered != 3 {
		t.Fatalf("source recovery failed: recovered=%d err=%v", recovered, err)
	}
	attachReplicaJournal(source, journal)

	destinationPath := filepath.Join(t.TempDir(), "destination.db")
	openDestination := func() (*organizerPolicy, *replicaReceiver, func()) {
		relay, db, err := newRelay(destinationPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
		receiver, err := newReplicaReceiver(policy, replicaScope{CityID: cityA, ServiceKey: nostr.GetPublicKey(service), Destination: destination})
		if err != nil {
			t.Fatal(err)
		}
		return policy, receiver, func() {
			relay.DisableExpirationManager()
			db.Close()
		}
	}
	destinationPolicy, receiver, closeDestination := openDestination()
	serviceEvent := nostr.Event{}
	serviceEvent.Sign(service)
	receiverContext := authCtx(t, serviceEvent)
	deliver := func(at time.Time) int {
		delivered, err := source.deliverReplicaOutbox(t.Context(), journal, at, func(_ context.Context, gotDestination string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
			if gotDestination != destination {
				t.Fatalf("unexpected destination: %s", gotDestination)
			}
			return receiver.receiveReplicaEnvelope(receiverContext, envelope)
		})
		if err != nil {
			t.Fatal(err)
		}
		return delivered
	}
	base := time.Unix(1900000000, 0)
	if delivered := deliver(base); delivered != 1 {
		t.Fatalf("initial occurrence delivery count: %d", delivered)
	}
	if destinationPolicy.checkCalendarRead(occurrence) != nil {
		t.Fatal("receiver did not expose the initial occurrence")
	}

	hide := eventModeration{CityID: cityA, Scope: "event", Target: calendarAddress(occurrence), EventID: occurrence.ID.Hex(), Status: "hidden", Reason: "Replicated moderation acceptance"}
	hidden := moderationEvent(t, admin, hide, 5)
	accept(t, sourceRelay, hidden)
	backfillJournal, err := openReplicaJournal(filepath.Join(t.TempDir(), "backfill-journal.db"), registry, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, err := source.recoverReplicaJournal(backfillJournal); err != nil || recovered != 4 {
		t.Fatalf("moderated source recovery failed: recovered=%d err=%v", recovered, err)
	}
	backfillOccurrence, found, err := backfillJournal.outboxEntry(occurrence.ID.Hex())
	if err != nil || !found || backfillOccurrence.Envelope == nil || replicaEnvelopeAction(*backfillOccurrence.Envelope) != "occurrence" {
		t.Fatalf("hidden occurrence was not retained ahead of its moderation: %#v %v", backfillOccurrence, err)
	}
	backfillModeration, found, err := backfillJournal.outboxEntry(hidden.ID.Hex())
	if err != nil || !found || backfillModeration.Envelope == nil || replicaEnvelopeAction(*backfillModeration.Envelope) != "moderation" || backfillModeration.SourceSequence <= backfillOccurrence.SourceSequence {
		t.Fatalf("recovered moderation was not ordered after its target: %#v %v", backfillModeration, err)
	}
	if err := backfillJournal.Close(); err != nil {
		t.Fatal(err)
	}
	failedAt := base.Add(time.Minute)
	if delivered, err := source.deliverReplicaOutbox(t.Context(), journal, failedAt, func(context.Context, string, replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		return replicaDeliveryAck{}, errors.New("simulated receiver outage")
	}); err != nil || delivered != 0 {
		t.Fatalf("outage attempt: delivered=%d err=%v", delivered, err)
	}
	row, found, err := journal.outboxEntry(hidden.ID.Hex())
	if err != nil || !found || row.Status != "retry" || row.RetryAt != failedAt.Unix()+30 || row.LastCode != "delivery-failed" {
		t.Fatalf("moderation retry was not durable and sanitized: %#v %v", row, err)
	}
	if delivered := deliver(failedAt.Add(31 * time.Second)); delivered != 1 {
		t.Fatalf("moderation retry delivery count: %d", delivered)
	}
	if destinationPolicy.checkCalendarRead(occurrence) == nil {
		t.Fatal("source-hidden occurrence stayed visible at receiver")
	}

	closeDestination()
	destinationPolicy, receiver, closeDestination = openDestination()
	defer closeDestination()
	if destinationPolicy.checkCalendarRead(occurrence) == nil {
		t.Fatal("receiver restart lost the hidden decision")
	}

	stale := hide
	stale.Status = "visible"
	stale.Previous = ""
	staleEvent := moderationEvent(t, admin, stale, 6)
	staleEnvelope := replicaDeliveryEnvelope{Version: replicaEnvelopeVersion, Action: "moderation", CityID: cityA, Destination: destination, SourceSequence: row.SourceSequence + 1, Event: staleEvent}
	if _, err := receiver.receiveReplicaEnvelope(receiverContext, staleEnvelope); err == nil {
		t.Fatal("receiver accepted stale moderation")
	}
	if _, found, err := receiver.importRecord(staleEvent.ID.Hex()); err != nil || found {
		t.Fatalf("rejected moderation advanced receiver state: found=%v err=%v", found, err)
	}

	hide.Status = "visible"
	hide.Previous = hidden.ID.Hex()
	visible := moderationEvent(t, admin, hide, 6)
	accept(t, sourceRelay, visible)
	if delivered := deliver(base.Add(2 * time.Minute)); delivered != 1 {
		t.Fatalf("visibility delivery count: %d", delivered)
	}
	if destinationPolicy.checkCalendarRead(occurrence) != nil {
		t.Fatal("ordered receiver visibility decision did not restore the walk")
	}

	cancellation := workflowEvent(t, creator, 5, "cancel", nostr.Tags{{"e", occurrence.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, 7)
	accept(t, sourceRelay, cancellation)
	if delivered := deliver(base.Add(3 * time.Minute)); delivered != 1 {
		t.Fatalf("cancellation delivery count: %d", delivered)
	}
	if destinationPolicy.checkCalendarRead(occurrence) == nil {
		t.Fatal("receiver cancellation did not suppress the walk")
	}

	hide.Status = "hidden"
	hide.Previous = visible.ID.Hex()
	hiddenAgain := moderationEvent(t, admin, hide, 8)
	accept(t, sourceRelay, hiddenAgain)
	hide.Status = "visible"
	hide.Previous = hiddenAgain.ID.Hex()
	visibleAgain := moderationEvent(t, admin, hide, 9)
	accept(t, sourceRelay, visibleAgain)
	if delivered := deliver(base.Add(4 * time.Minute)); delivered != 2 {
		t.Fatalf("post-cancellation moderation delivery count: %d", delivered)
	}
	if destinationPolicy.checkCalendarRead(occurrence) == nil {
		t.Fatal("receiver unhide resurrected a canceled occurrence")
	}

	canceledRecovery, err := openReplicaJournal(filepath.Join(t.TempDir(), "canceled-recovery.db"), registry, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.recoverReplicaJournal(canceledRecovery); err != nil {
		t.Fatal(err)
	}
	for _, decision := range []nostr.Event{hidden, visible, hiddenAgain, visibleAgain} {
		row, found, err := canceledRecovery.outboxEntry(decision.ID.Hex())
		if err != nil || !found || row.Status != "suppressed" || row.Attempts != 0 {
			t.Fatalf("historical moderation for an undelivered cancellation remained actionable: %#v %v", row, err)
		}
	}
	if err := canceledRecovery.Close(); err != nil {
		t.Fatal(err)
	}
}
