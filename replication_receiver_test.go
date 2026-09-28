package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestReplicaBundleAllowsRetainedPastOccurrence(t *testing.T) {
	admin, creator, service := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "source.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	revision := draftEvent(t, creator, cityA, 0)
	accept(t, relay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, relay, approval)

	// Simulate an occurrence that was accepted while it was in the publication
	// horizon, retained in the authoritative DB, and recovered after it ended.
	past := replicatedOccurrence(t, creator, revision, approval, "2026-01-01", 3)
	start := time.Now().Add(-2 * time.Hour).Unix()
	values := map[string]string{
		"d":     cityA + ":" + time.Unix(start, 0).UTC().Format("2006-01-02"),
		"start": strconv.FormatInt(start, 10),
		"end":   strconv.FormatInt(start+3600, 10),
		"D":     strconv.FormatInt(start/86400, 10),
	}
	for i := range past.Tags {
		if value, ok := values[past.Tags[i][0]]; ok {
			past.Tags[i][1] = value
		}
	}
	past.Sign(creator)
	if err := db.SaveEvent(past); err != nil {
		t.Fatal(err)
	}
	if err := policy.checkOrganizerCalendar(past, true); err == nil {
		t.Fatal("ordinary publication path accepted a new past occurrence")
	}
	bundle, err := policy.exportReplicaBundle(cityA, past.ID.Hex())
	if err != nil {
		t.Fatalf("retained past occurrence was not exportable: %v", err)
	}
	scope := replicaScope{CityID: cityA, ServiceKey: nostr.GetPublicKey(service), Destination: "wss://madeira.example/"}
	serviceEvent := nostr.Event{}
	serviceEvent.Sign(service)
	if err := validateReplicaBundle(authCtx(t, serviceEvent), bundle, scope, adminPK); err != nil {
		t.Fatalf("retained past occurrence bundle was not valid at receiver: %v", err)
	}
}

func TestReplicaReceiverStagesInstallsAndAcknowledgesExactly(t *testing.T) {
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
	grantEvent := workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1)
	accept(t, sourceRelay, grantEvent)
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, sourceRelay, approval)

	makeEnvelope := func(date string, offset int, sequence uint64) replicaDeliveryEnvelope {
		t.Helper()
		occurrence := replicatedOccurrence(t, creator, revision, approval, date, offset)
		accept(t, sourceRelay, occurrence)
		bundle, err := source.exportReplicaBundle(cityA, occurrence.ID.Hex())
		if err != nil {
			t.Fatal(err)
		}
		return replicaDeliveryEnvelope{
			Version:           replicaEnvelopeVersion,
			CityID:            cityA,
			Destination:       destination,
			OccurrenceID:      occurrence.ID.Hex(),
			SourceSequence:    sequence,
			CurrentApprovalID: approval.ID.Hex(),
			CurrentGrantID:    grantEvent.ID.Hex(),
			Event:             occurrence,
			Bundle:            bundle,
		}
	}

	destinationRelay, destinationDB, err := newRelay(filepath.Join(t.TempDir(), "destination.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer destinationDB.Close()
	defer destinationRelay.DisableExpirationManager()
	destinationPolicy := unitOrganizerPolicy(destinationRelay, enableOrganizers(destinationRelay, destinationDB, adminPK))
	scope := replicaScope{CityID: cityA, ServiceKey: nostr.GetPublicKey(service), Destination: destination}
	receiver, err := newReplicaReceiver(destinationPolicy, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := attachReplicaReceiverTransport(destinationRelay, receiver); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(destinationRelay)
	defer server.Close()
	connector := func(ctx context.Context, requested string, options nostr.RelayOptions) (*nostr.Relay, error) {
		if requested != destination {
			t.Fatalf("transport dialed an unconfigured destination: %s", requested)
		}
		return nostr.RelayConnect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), options)
	}
	transport := func(ctx context.Context, destination string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		return deliverReplicaEnvelopeWSS(ctx, destination, envelope, service, connector)
	}
	serviceEvent := nostr.Event{}
	serviceEvent.Sign(service)
	ctx := authCtx(t, serviceEvent)

	first := makeEnvelope("2026-10-10", 3, 10)
	badDestination := first
	badDestination.Destination = "wss://other.example/"
	if _, err := receiver.receiveReplicaEnvelope(ctx, badDestination); err == nil {
		t.Fatal("receiver accepted a delivery for another destination")
	}
	if _, found, err := receiver.importRecord(first.OccurrenceID); err != nil || found {
		t.Fatalf("invalid delivery changed receiver state: found=%v err=%v", found, err)
	}
	if _, err := receiver.receiveReplicaEnvelope(context.Background(), first); err == nil {
		t.Fatal("receiver accepted an unauthenticated delivery")
	}
	wrongService := nostr.Generate()
	if _, err := deliverReplicaEnvelopeWSS(t.Context(), destination, first, wrongService, connector); err == nil {
		t.Fatal("WSS receiver accepted another authenticated service identity")
	}

	// Fail only when the public target is reached. Dependencies may have been
	// installed, but the occurrence stays invisible and the durable staged row
	// makes the exact authenticated retry resumable.
	originalSave := receiver.saveEvent
	failedTarget := false
	receiver.saveEvent = func(event nostr.Event) error {
		if event.ID == first.Event.ID && !failedTarget {
			failedTarget = true
			return errors.New("injected target write failure")
		}
		return originalSave(event)
	}
	if ack, err := transport(ctx, destination, first); err == nil || ack.Accepted {
		t.Fatalf("partial installation was acknowledged: %#v %v", ack, err)
	} else if !strings.Contains(err.Error(), "injected target write failure") {
		t.Fatalf("delivery failed before reaching the receiver: %v", err)
	}
	if destinationPolicy.byID(first.OccurrenceID) != nil {
		t.Fatal("target became visible after its write failed")
	}
	record, found, err := receiver.importRecord(first.OccurrenceID)
	if err != nil || !found || record.Status != "staged" {
		t.Fatalf("failed installation was not durably staged: %#v %v", record, err)
	}

	second := makeEnvelope("2026-10-17", 4, 12)
	if _, err := receiver.receiveReplicaEnvelope(ctx, second); err == nil {
		t.Fatal("receiver skipped an incomplete earlier import")
	}
	if _, found, err := receiver.importRecord(second.OccurrenceID); err != nil || found {
		t.Fatalf("blocked later delivery changed receiver state: found=%v err=%v", found, err)
	}

	receiver.saveEvent = originalSave
	ack, err := transport(ctx, destination, first)
	if err != nil || !ack.Accepted || ack.EventID != first.OccurrenceID || ack.SourceSequence != first.SourceSequence {
		t.Fatalf("exact retry did not complete: %#v %v", ack, err)
	}
	if destinationPolicy.byID(first.OccurrenceID) == nil {
		t.Fatal("acknowledged occurrence is unavailable in destination storage")
	}
	for event := range destinationDB.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{replicaTransportKind}}, 1) {
		t.Fatalf("private transport wrapper was stored: %s", event.ID.Hex())
	}
	record, found, err = receiver.importRecord(first.OccurrenceID)
	if err != nil || !found || record.Status != "installed" {
		t.Fatalf("completed installation state missing: %#v %v", record, err)
	}

	ack, err = receiver.receiveReplicaEnvelope(ctx, first)
	if err != nil || !ack.Accepted || ack.EventID != first.OccurrenceID {
		t.Fatalf("exact duplicate was not idempotent: %#v %v", ack, err)
	}
	ack, err = receiver.receiveReplicaEnvelope(ctx, second)
	if err != nil || !ack.Accepted || ack.SourceSequence != second.SourceSequence {
		t.Fatalf("next ordered delivery failed: %#v %v", ack, err)
	}
	replacement := makeEnvelope("2026-10-17", 5, 13)
	ack, err = receiver.receiveReplicaEnvelope(ctx, replacement)
	if err != nil || !ack.Accepted || ack.SourceSequence != replacement.SourceSequence {
		t.Fatalf("addressable replacement delivery failed: %#v %v", ack, err)
	}
	if destinationPolicy.byID(second.OccurrenceID) != nil || destinationPolicy.byID(replacement.OccurrenceID) == nil {
		t.Fatal("replica receiver retained a superseded addressable occurrence")
	}
	ack, err = receiver.receiveReplicaEnvelope(ctx, second)
	if err != nil || !ack.Accepted || ack.SourceSequence != second.SourceSequence {
		t.Fatalf("superseded exact replay was not idempotent: %#v %v", ack, err)
	}
	reopened, err := newReplicaReceiver(destinationPolicy, scope)
	if err != nil {
		t.Fatal(err)
	}
	ack, err = reopened.receiveReplicaEnvelope(ctx, first)
	if err != nil || !ack.Accepted || ack.SourceSequence != first.SourceSequence {
		t.Fatalf("durable older exact replay was not idempotent: %#v %v", ack, err)
	}

	conflict := makeEnvelope("2026-10-24", 5, second.SourceSequence)
	if _, err := receiver.receiveReplicaEnvelope(ctx, conflict); err == nil {
		t.Fatal("receiver accepted a different event at a stale checkpoint")
	}
	if destinationPolicy.byID(conflict.OccurrenceID) != nil {
		t.Fatal("conflicting checkpoint installed an occurrence")
	}

	cancellation := nostr.Event{Kind: 5, CreatedAt: first.Event.CreatedAt + 1, Tags: nostr.Tags{{"e", first.OccurrenceID}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: replicated organizer cancellation"}
	cancellation.Sign(creator)
	accept(t, sourceRelay, cancellation)
	cancellationEnvelope := replicaDeliveryEnvelope{Version: replicaEnvelopeVersion, Action: "cancellation", CityID: cityA, Destination: destination, OccurrenceID: first.OccurrenceID, SourceSequence: 14, Event: cancellation}
	ack, err = transport(ctx, destination, cancellationEnvelope)
	if err != nil || !ack.Accepted || ack.EventID != cancellation.ID.Hex() || ack.SourceSequence != 14 {
		t.Fatalf("ordered cancellation was not installed: %#v %v", ack, err)
	}
	if destinationPolicy.checkCalendarRead(first.Event) == nil {
		t.Fatal("replicated cancellation left its occurrence public")
	}
	if err := destinationPolicy.checkCalendarRead(replacement.Event); err != nil {
		t.Fatalf("cancellation hid an unrelated occurrence: %v", err)
	}

	revocation := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "revoked"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "revoked"}}, 20)
	accept(t, sourceRelay, revocation)
	revocationEnvelope := replicaDeliveryEnvelope{Version: replicaEnvelopeVersion, Action: "revocation", CityID: cityA, Destination: destination, SourceSequence: 15, Event: revocation}
	ack, err = transport(ctx, destination, revocationEnvelope)
	if err != nil || !ack.Accepted || ack.EventID != revocation.ID.Hex() || ack.SourceSequence != 15 {
		t.Fatalf("ordered revocation was not installed: %#v %v", ack, err)
	}
	if destinationPolicy.checkCalendarRead(replacement.Event) == nil {
		t.Fatal("replicated city revocation left an occurrence public")
	}
	ack, err = transport(ctx, destination, revocationEnvelope)
	if err != nil || !ack.Accepted || ack.EventID != revocation.ID.Hex() {
		t.Fatalf("exact revocation replay was not idempotent: %#v %v", ack, err)
	}

	changedScope := scope
	changedScope.ServiceKey = nostr.GetPublicKey(nostr.Generate())
	if _, err := newReplicaReceiver(destinationPolicy, changedScope); err == nil {
		t.Fatal("receiver silently changed its operator-owned service identity")
	}

	for _, suppression := range []struct {
		name  string
		event func() nostr.Event
	}{
		{
			name: "retained destination tombstone",
			event: func() nostr.Event {
				event := nostr.Event{Kind: 5, CreatedAt: first.Event.CreatedAt + 100, Tags: nostr.Tags{{"e", first.OccurrenceID}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: retained destination state"}
				event.Sign(creator)
				return event
			},
		},
		{
			name: "newer destination revocation",
			event: func() nostr.Event {
				return workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "revoked"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "revoked"}}, 100)
			},
		},
	} {
		t.Run(suppression.name, func(t *testing.T) {
			relay, db, err := newRelay(filepath.Join(t.TempDir(), "suppressed.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			defer relay.DisableExpirationManager()
			policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
			if err := db.SaveEvent(suppression.event()); err != nil {
				t.Fatal(err)
			}
			suppressedReceiver, err := newReplicaReceiver(policy, scope)
			if err != nil {
				t.Fatal(err)
			}
			if ack, err := suppressedReceiver.receiveReplicaEnvelope(ctx, first); err == nil || ack.Accepted {
				t.Fatalf("receiver resurrected suppressed occurrence: %#v %v", ack, err)
			}
			if policy.byID(first.OccurrenceID) != nil {
				t.Fatal("suppressed occurrence entered destination storage")
			}
		})
	}
}
