package main

import (
	"encoding/json"
	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestReviewedProductionHistoryRehearsal(t *testing.T) {
	path := os.Getenv("BITCOINWALK_PUBLIC_HISTORY_FIXTURE")
	if path == "" {
		t.Skip("requires explicit read-only public snapshot artifact")
	}
	originalsPath := os.Getenv("BITCOINWALK_ORIGINALS_FIXTURE")
	var events, originals []nostr.Event
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(originalsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &originals); err != nil {
		t.Fatal(err)
	}
	if len(originals) != 3 {
		t.Fatal("expected three reviewed originals")
	}
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "source.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	for _, e := range events {
		if !e.CheckID() || !e.VerifySignature() {
			t.Fatal("invalid signed snapshot event")
		}
		if err := db.SaveEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	p := unitOrganizerPolicy(relay, enableOrganizers(relay, db, nostr.MustPubKeyFromHex(adminHex)))
	// London has retained historical dependencies in its existing live journal;
	// this fresh-journal rehearsal covers the four newly activated receivers.
	destinations := map[string]string{"83f15504-9fca-4d59-a0f6-616a559510df": "wss://madeira.bitcoinwalk.org/", "e62592ac-a5fc-42df-8f31-94416f6ee8d5": "wss://graz.bitcoinwalk.org/", "d42123e1-1d01-473b-98e7-846342150eca": "wss://hanover.bitcoinwalk.org/", "8e136ccb-923b-41aa-a6d1-1290b0b3a014": "wss://wuerzburg.bitcoinwalk.org/"}
	j, err := openReplicaJournal(filepath.Join(t.TempDir(), "journal.db"), &replicaRegistry{destinations: destinations}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	err = j.db.Update(func(tx *bbolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists(replicaInitialArchiveBucket)
		if e != nil {
			return e
		}
		for _, event := range originals {
			data, e := json.Marshal(event)
			if e != nil {
				return e
			}
			if e = b.Put([]byte(event.ID.Hex()), data); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.recoverReplicaJournal(j); err != nil {
		t.Fatal(err)
	}
	attachReplicaJournal(p, j)
	if output := os.Getenv("BITCOINWALK_REHEARSAL_EXPORT_DIR"); output != "" {
		if err := db.DB.View(func(tx *bbolt.Tx) error { return tx.CopyFile(filepath.Join(output, "events.db"), 0600) }); err != nil {
			t.Fatal(err)
		}
		if err := j.db.View(func(tx *bbolt.Tx) error { return tx.CopyFile(filepath.Join(output, "journal.db"), 0600) }); err != nil {
			t.Fatal(err)
		}
	}
	service := nostr.Generate()
	auth := nostr.Event{}
	auth.Sign(service)
	total := 0
	for city, destination := range destinations {
		destinationRelay, destinationDB, err := newRelay(filepath.Join(t.TempDir(), "receiver.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		destinationPolicy := unitOrganizerPolicy(destinationRelay, enableOrganizers(destinationRelay, destinationDB, p.admin))
		scope := replicaScope{CityID: city, Destination: destination, ServiceKey: service.Public()}
		receiver, err := newReplicaReceiver(destinationPolicy, scope)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		envelopes := []replicaDeliveryEnvelope{}
		for event := range db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{31923}, Tags: nostr.TagMap{"i": {city}}}, 1000) {
			if p.checkCalendarRead(event) != nil {
				continue
			}
			row, found, err := j.outboxEntry(event.ID.Hex())
			if err != nil || !found || row.Envelope == nil {
				t.Fatalf("%s missing eligible envelope for %s", city, event.ID.Hex())
			}
			envelopes = append(envelopes, *row.Envelope)
			count++
		}
		slices.SortFunc(envelopes, func(a, b replicaDeliveryEnvelope) int {
			if a.SourceSequence < b.SourceSequence {
				return -1
			}
			if a.SourceSequence > b.SourceSequence {
				return 1
			}
			return 0
		})
		for _, envelope := range envelopes {
			if _, err := receiver.receiveReplicaEnvelope(authCtx(t, auth), envelope); err != nil {
				t.Fatalf("%s: %v", city, err)
			}
		}
		public := 0
		for event := range destinationDB.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{31923}}, 1000) {
			if destinationPolicy.checkCalendarRead(event) == nil {
				public++
			}
		}
		if public != count {
			t.Fatalf("%s source=%d replica=%d", city, count, public)
		}
		t.Logf("%s: %d exact signed public walks installed", city, count)
		total += count
		destinationRelay.DisableExpirationManager()
		destinationDB.Close()
	}
	if total == 0 {
		t.Fatal("no public walks rehearsed")
	}
}
