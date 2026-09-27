package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestApprovedInitialWalkIsReleasedAndReplicatedImmediately(t *testing.T) {
	admin, creator, service := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := admin.Public(), creator.Public()
	destination := "wss://firstwalk.example/"
	sourceRelay, sourceDB, err := newRelay(filepath.Join(t.TempDir(), "source.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	defer sourceRelay.DisableExpirationManager()
	source := unitOrganizerPolicy(sourceRelay, enableOrganizers(sourceRelay, sourceDB, adminPK))
	registry := &replicaRegistry{destinations: map[string]string{cityA: destination}}
	journal, err := openReplicaJournal(filepath.Join(t.TempDir(), "journal.db"), registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if recovered, err := source.recoverReplicaJournal(journal); err != nil || recovered != 0 {
		t.Fatalf("initialize journal: recovered=%d err=%v", recovered, err)
	}
	attachReplicaJournal(source, journal)

	start := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	initial := nostr.Event{Kind: 31923, CreatedAt: nostr.Now(), Content: "Test walk", Tags: nostr.Tags{
		{"d", cityA + ":" + start.Format("2006-01-02")}, {"title", "BitcoinWalk Test City"}, {"summary", "BitcoinWalk in Test City"}, {"image", "https://example.com/image.jpg"},
		{"start", strconv.FormatInt(start.Unix(), 10)}, {"D", strconv.FormatInt(start.Unix()/86400, 10)}, {"location", "Square"}, {"location", "1,2"}, {"t", "bitcoinwalk"}, {"r", "https://example.com/chat"},
		{"end", strconv.FormatInt(start.Add(time.Hour).Unix(), 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "initial-proposal-v1"},
	}}
	initial.Sign(creator)
	accept(t, sourceRelay, initial)
	row, found, err := journal.outboxEntry(initial.ID.Hex())
	if err != nil || !found || row.Status != "blocked" || row.Envelope != nil {
		t.Fatalf("hidden initial walk was not blocked: %#v %v", row, err)
	}

	revision := draftEvent(t, creator, cityA, 1)
	var city map[string]any
	if json.Unmarshal([]byte(revision.Content), &city) != nil {
		t.Fatal("draft JSON")
	}
	city["startAt"] = start.Format(time.RFC3339)
	body, _ := json.Marshal(city)
	revision.Content = string(body)
	revision.Tags = append(revision.Tags, nostr.Tag{"e", initial.ID.Hex(), "", "initial-walk"})
	revision.Sign(creator)
	accept(t, sourceRelay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	grantEvent := workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 2)
	accept(t, sourceRelay, grantEvent)
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), InitialEventID: initial.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"e", initial.ID.Hex(), "", "initial-walk"}, {"status", "approved"}}, 3)
	accept(t, sourceRelay, approval)

	row, found, err = journal.outboxEntry(initial.ID.Hex())
	if err != nil || !found || row.Status != "pending" || row.Envelope == nil || row.Envelope.Event.ID != initial.ID {
		t.Fatalf("approval did not release the exact initial walk: %#v %v", row, err)
	}
	delivered, err := source.deliverReplicaOutbox(t.Context(), journal, time.Now(), func(_ context.Context, gotDestination string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		if gotDestination != destination || envelope.Event.ID != initial.ID {
			t.Fatal("delivery worker received the wrong first-walk target")
		}
		return replicaDeliveryAck{EventID: initial.ID.Hex(), SourceSequence: envelope.SourceSequence, Accepted: true}, nil
	})
	if err != nil || delivered != 1 {
		t.Fatalf("delivery worker did not refresh the released first walk: delivered=%d err=%v", delivered, err)
	}
	row, found, err = journal.outboxEntry(initial.ID.Hex())
	if err != nil || !found || row.Status != "acknowledged" || row.Attempts != 1 || row.Envelope == nil {
		t.Fatalf("released first walk was not acknowledged: %#v %v", row, err)
	}
	serviceEvent := nostr.Event{}
	serviceEvent.Sign(service)
	scope := replicaScope{CityID: cityA, ServiceKey: service.Public(), Destination: destination}
	if _, err := validateReplicaEnvelope(authCtx(t, serviceEvent), *row.Envelope, scope, adminPK); err != nil {
		t.Fatalf("released initial walk envelope failed validation: %v", err)
	}

	destinationRelay, destinationDB, err := newRelay(filepath.Join(t.TempDir(), "destination.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer destinationDB.Close()
	defer destinationRelay.DisableExpirationManager()
	destinationPolicy := unitOrganizerPolicy(destinationRelay, enableOrganizers(destinationRelay, destinationDB, adminPK))
	receiver, err := newReplicaReceiver(destinationPolicy, scope)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := receiver.receiveReplicaEnvelope(authCtx(t, serviceEvent), *row.Envelope)
	if err != nil || !ack.Accepted || ack.EventID != initial.ID.Hex() {
		t.Fatalf("receiver rejected released initial walk: %#v %v", ack, err)
	}
	stored := destinationPolicy.byID(initial.ID.Hex())
	if stored == nil || stored.ID != initial.ID || destinationPolicy.checkCalendarRead(*stored) != nil {
		t.Fatal("released initial walk is not publicly readable at the destination")
	}

	revocation := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "revoked"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "revoked"}}, 4)
	accept(t, sourceRelay, revocation)
	if recovered, err := source.recoverReplicaJournal(journal); err != nil || recovered != 0 {
		t.Fatalf("restart reconciliation failed for revoked first walk: recovered=%d err=%v", recovered, err)
	}
	row, found, err = journal.outboxEntry(initial.ID.Hex())
	if err != nil || !found || row.Status != "revoked" {
		t.Fatalf("revoked first walk was not durably suppressed: %#v %v", row, err)
	}
}
