package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

func putReplayRow(t *testing.T, db *bbolt.DB, row replicaOutboxRow) {
	t.Helper()
	if err := db.Update(func(tx *bbolt.Tx) error { return putOutboxRow(tx, row) }); err != nil {
		t.Fatal(err)
	}
}

func replayJournalFixture(t *testing.T, status string) (string, replicaDeliveryEnvelope) {
	t.Helper()
	key := nostr.Generate()
	event := nostr.Event{Kind: 31923, Content: "retained occurrence", Tags: nostr.Tags{{"i", cityA}, {"bitcoinwalk", "occurrence-v1"}}}
	event.Sign(key)
	envelope := replicaDeliveryEnvelope{
		Version:           replicaEnvelopeVersion,
		Action:            "occurrence",
		CityID:            cityA,
		Destination:       "wss://replica.example/",
		OccurrenceID:      event.ID.Hex(),
		SourceSequence:    7,
		CurrentApprovalID: "approval",
		CurrentGrantID:    "grant",
		Event:             event,
		Bundle:            []byte(`{"version":1}`),
	}
	path := filepath.Join(t.TempDir(), "journal.db")
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucket(replicaOutboxBucket); err != nil {
			return err
		}
		return putOutboxRow(tx, replicaOutboxRow{
			EventID:        event.ID.Hex(),
			CityID:         cityA,
			Destination:    envelope.Destination,
			SourceSequence: envelope.SourceSequence,
			Status:         status,
			Attempts:       1,
			Envelope:       &envelope,
		})
	})
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return path, envelope
}

func TestReplicaReplayIsReadOnlyAndRequiresExactAcknowledgement(t *testing.T) {
	path, envelope := replayJournalFixture(t, "acknowledged")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := func(_ context.Context, destination string, delivered replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		calls++
		if destination != envelope.Destination || !receiverEnvelopeEqual(delivered, envelope) {
			t.Fatal("replay changed the stored destination or envelope")
		}
		return replicaDeliveryAck{EventID: envelope.OccurrenceID, SourceSequence: envelope.SourceSequence, Accepted: true}, nil
	}
	ack, err := replayAcknowledgedReplica(t.Context(), path, "", envelope.OccurrenceID, replicaReplayConfirmation, transport)
	if err != nil || !ack.Accepted || calls != 1 {
		t.Fatalf("exact replay failed: %#v calls=%d err=%v", ack, calls, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("read-only replay changed the source journal")
	}

	badAck := func(context.Context, string, replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		return replicaDeliveryAck{EventID: envelope.OccurrenceID, SourceSequence: envelope.SourceSequence + 1, Accepted: true}, nil
	}
	if _, err := replayAcknowledgedReplica(t.Context(), path, "", envelope.OccurrenceID, replicaReplayConfirmation, badAck); err == nil {
		t.Fatal("replay accepted an inexact receiver acknowledgement")
	}
	failed := func(context.Context, string, replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		return replicaDeliveryAck{}, errors.New("receiver unavailable")
	}
	if _, err := replayAcknowledgedReplica(t.Context(), path, "", envelope.OccurrenceID, replicaReplayConfirmation, failed); err == nil {
		t.Fatal("replay hid a transport failure")
	}
}

func TestReplicaReplayFailsClosed(t *testing.T) {
	path, envelope := replayJournalFixture(t, "pending")
	called := false
	transport := func(context.Context, string, replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		called = true
		return replicaDeliveryAck{}, nil
	}
	if _, err := replayAcknowledgedReplica(t.Context(), path, "", envelope.OccurrenceID, replicaReplayConfirmation, transport); err == nil || called {
		t.Fatal("replayed an unacknowledged outbox row")
	}
	if _, err := replayAcknowledgedReplica(t.Context(), path, "", envelope.OccurrenceID, "wrong", transport); err == nil || called {
		t.Fatal("replay accepted the wrong operator confirmation")
	}
}

func TestReplicaShadowBackfillIsReadOnlyOrderedAndExcludesSuppressedRows(t *testing.T) {
	path, later := replayJournalFixture(t, "acknowledged")
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	earlier := later
	earlier.Event = nostr.Event{Kind: 31923, Content: "earlier retained occurrence", Tags: nostr.Tags{{"i", cityA}, {"bitcoinwalk", "occurrence-v1"}}}
	earlier.Event.Sign(nostr.Generate())
	earlier.OccurrenceID = earlier.Event.ID.Hex()
	earlier.SourceSequence = 3
	putReplayRow(t, db, replicaOutboxRow{EventID: earlier.OccurrenceID, CityID: cityA, Destination: earlier.Destination, SourceSequence: earlier.SourceSequence, Status: "acknowledged", Attempts: 1, Envelope: &earlier})
	suppressed := later
	suppressed.Event = nostr.Event{Kind: 31923, Content: "canceled occurrence", Tags: nostr.Tags{{"i", cityA}, {"bitcoinwalk", "occurrence-v1"}}}
	suppressed.Event.Sign(nostr.Generate())
	suppressed.OccurrenceID = suppressed.Event.ID.Hex()
	suppressed.SourceSequence = 5
	putReplayRow(t, db, replicaOutboxRow{EventID: suppressed.OccurrenceID, CityID: cityA, Destination: suppressed.Destination, SourceSequence: 9, Status: "canceled", Attempts: 2, Envelope: &suppressed})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	target := "wss://candidate.example/"
	var delivered []replicaDeliveryEnvelope
	transport := func(_ context.Context, destination string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		if destination != target || envelope.Destination != target {
			t.Fatalf("shadow delivery used wrong destination: %q %#v", destination, envelope)
		}
		delivered = append(delivered, envelope)
		return replicaDeliveryAck{EventID: envelope.OccurrenceID, SourceSequence: envelope.SourceSequence, Accepted: true}, nil
	}
	result, err := shadowBackfillReplica(t.Context(), path, "", cityA, later.Destination, target, replicaShadowBackfillConfirmation, transport)
	if err != nil {
		t.Fatal(err)
	}
	if result.Delivered != 2 || len(delivered) != 2 || delivered[0].SourceSequence != 3 || delivered[1].SourceSequence != 7 {
		t.Fatalf("unexpected ordered shadow result: %#v delivered=%#v", result, delivered)
	}
	if delivered[0].OccurrenceID != earlier.OccurrenceID || delivered[1].OccurrenceID != later.OccurrenceID {
		t.Fatal("shadow backfill included a suppressed row or changed ordering")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("shadow backfill changed the source journal")
	}
}

func TestReplicaShadowBackfillFailsClosedBeforeDelivery(t *testing.T) {
	path, envelope := replayJournalFixture(t, "pending")
	called := false
	transport := func(context.Context, string, replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		called = true
		return replicaDeliveryAck{}, nil
	}
	for _, test := range []struct {
		name         string
		city         string
		source       string
		candidate    string
		confirmation string
	}{
		{"unsettled outbox", cityA, envelope.Destination, "wss://candidate.example/", replicaShadowBackfillConfirmation},
		{"wrong confirmation", cityA, envelope.Destination, "wss://candidate.example/", "wrong"},
		{"same destination", cityA, envelope.Destination, envelope.Destination, replicaShadowBackfillConfirmation},
		{"invalid city", "not-a-city", envelope.Destination, "wss://candidate.example/", replicaShadowBackfillConfirmation},
	} {
		t.Run(test.name, func(t *testing.T) {
			called = false
			if _, err := shadowBackfillReplica(t.Context(), path, "", test.city, test.source, test.candidate, test.confirmation, transport); err == nil || called {
				t.Fatal("unsafe shadow backfill reached the transport")
			}
		})
	}
}

func TestReplicaAlertRehearsalArmsExactAcknowledgedOccurrence(t *testing.T) {
	path, envelope := replayJournalFixture(t, "acknowledged")
	row, err := armAcknowledgedReplicaAlertRehearsal(path, envelope.OccurrenceID, replicaAlertRehearsalConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "pending" || row.EventID != envelope.OccurrenceID || row.Attempts != 1 || row.RetryAt != 0 || row.LastCode != "" {
		t.Fatalf("unexpected armed row: %#v", row)
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var stored *replicaOutboxRow
	if err := db.View(func(tx *bbolt.Tx) error {
		var found bool
		var readErr error
		stored, found, readErr = outboxRow(tx, envelope.OccurrenceID)
		if readErr != nil {
			return readErr
		}
		if !found {
			return errors.New("armed row disappeared")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stored.Status != "pending" || stored.Envelope == nil || !receiverEnvelopeEqual(*stored.Envelope, envelope) {
		t.Fatalf("rehearsal changed the retained envelope: %#v", stored)
	}
}

func TestReplicaAlertRehearsalFailsClosed(t *testing.T) {
	path, envelope := replayJournalFixture(t, "acknowledged")
	if _, err := armAcknowledgedReplicaAlertRehearsal(path, envelope.OccurrenceID, "wrong"); err == nil {
		t.Fatal("accepted the wrong rehearsal confirmation")
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.View(func(tx *bbolt.Tx) error {
		row, found, readErr := outboxRow(tx, envelope.OccurrenceID)
		if readErr != nil || !found {
			return readErr
		}
		status = row.Status
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if status != "acknowledged" {
		t.Fatalf("failed confirmation changed status to %q", status)
	}

	pendingPath, pending := replayJournalFixture(t, "pending")
	if _, err := armAcknowledgedReplicaAlertRehearsal(pendingPath, pending.OccurrenceID, replicaAlertRehearsalConfirmation); err == nil {
		t.Fatal("armed a row that was not acknowledged")
	}
}
