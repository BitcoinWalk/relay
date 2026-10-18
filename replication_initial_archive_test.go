package main

import (
	"encoding/json"
	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestArchivedInitialReconcilesAndBuildsExactBundleWithoutResurrectingWalk(t *testing.T) {
	t.Run("future", func(t *testing.T) { testArchivedInitial(t, 48*time.Hour) })
	t.Run("past", func(t *testing.T) { testArchivedInitial(t, -48*time.Hour) })
}

func testArchivedInitial(t *testing.T, offset time.Duration) {
	admin, creator := nostr.Generate(), nostr.Generate()
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "source.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	p := unitOrganizerPolicy(relay, enableOrganizers(relay, db, admin.Public()))
	start := time.Now().Add(offset).Truncate(time.Second)
	initial := nostr.Event{Kind: 31923, CreatedAt: nostr.Now(), Content: "Test walk", Tags: nostr.Tags{
		{"d", cityA + ":" + start.Format("2006-01-02")}, {"title", "BitcoinWalk Test City"}, {"summary", "BitcoinWalk in Test City"}, {"image", "https://example.com/image.jpg"},
		{"start", strconv.FormatInt(start.Unix(), 10)}, {"D", strconv.FormatInt(start.Unix()/86400, 10)}, {"location", "Square"}, {"location", "1,2"}, {"t", "bitcoinwalk"}, {"r", "https://example.com/chat"},
		{"end", strconv.FormatInt(start.Add(time.Hour).Unix(), 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "initial-proposal-v1"}}}
	initial.Sign(creator)
	if err := db.SaveEvent(initial); err != nil {
		t.Fatal(err)
	}
	if offset < 0 && p.validateInitialProposal(initial, nil, true) == nil {
		t.Fatal("ordinary publication allowed a new past walk")
	}
	revision := draftEvent(t, creator, cityA, 1)
	var city map[string]any
	json.Unmarshal([]byte(revision.Content), &city)
	city["startAt"] = start.Format(time.RFC3339)
	body, _ := json.Marshal(city)
	revision.Content = string(body)
	revision.Tags = append(revision.Tags, nostr.Tag{"e", initial.ID.Hex(), "", "initial-walk"})
	revision.Sign(creator)
	if err := db.SaveEvent(revision); err != nil {
		t.Fatal(err)
	}
	grant := cityGrant{CityID: cityA, CreatorPubkey: creator.Public().Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creator.Public().Hex()}, SuperAdminPubkey: admin.Public().Hex()}
	if err := db.SaveEvent(workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 2)); err != nil {
		t.Fatal(err)
	}
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), InitialEventID: initial.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"e", initial.ID.Hex(), "", "initial-walk"}, {"status", "approved"}}, 3)
	if err := db.SaveEvent(approval); err != nil {
		t.Fatal(err)
	}
	latest := replicatedOccurrence(t, creator, revision, approval, time.Now().Add(48*time.Hour).Format("2006-01-02"), 4)
	accept(t, relay, latest)
	if err := db.DeleteEvent(initial.ID); err != nil {
		t.Fatal(err)
	}
	j, err := openReplicaJournal(filepath.Join(t.TempDir(), "journal.db"), &replicaRegistry{destinations: map[string]string{cityA: "wss://archive.example/"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if _, err := p.recoverReplicaJournal(j); err == nil {
		t.Fatal("missing dependency was silently accepted")
	}
	err = j.db.Update(func(tx *bbolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists(replicaInitialArchiveBucket)
		if e != nil {
			return e
		}
		data, e := json.Marshal(initial)
		if e != nil {
			return e
		}
		return b.Put([]byte(initial.ID.Hex()), data)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.recoverReplicaJournal(j); err != nil {
		t.Fatal(err)
	}
	attachReplicaJournal(p, j)
	if p.byID(initial.ID.Hex()) != nil {
		t.Fatal("historical dependency was resurrected in source storage")
	}
	if _, found, _ := j.outboxEntry(initial.ID.Hex()); found {
		t.Fatal("archive original was queued for public delivery")
	}
	bundle, err := p.exportReplicaBundle(cityA, latest.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	service := nostr.Generate()
	scope := replicaScope{CityID: cityA, ServiceKey: service.Public(), Destination: "wss://archive.example/"}
	authEvent := nostr.Event{}
	authEvent.Sign(service)
	if err := validateReplicaBundle(authCtx(t, authEvent), bundle, scope, admin.Public()); err != nil {
		t.Fatal(err)
	}
	destinationRelay, destinationDB, err := newRelay(filepath.Join(t.TempDir(), "destination.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer destinationDB.Close()
	defer destinationRelay.DisableExpirationManager()
	destinationPolicy := unitOrganizerPolicy(destinationRelay, enableOrganizers(destinationRelay, destinationDB, admin.Public()))
	receiver, err := newReplicaReceiver(destinationPolicy, scope)
	if err != nil {
		t.Fatal(err)
	}
	row, found, err := j.outboxEntry(latest.ID.Hex())
	if err != nil || !found || row.Envelope == nil {
		t.Fatal("missing current occurrence envelope")
	}
	if _, err := receiver.receiveReplicaEnvelope(authCtx(t, authEvent), *row.Envelope); err != nil {
		t.Fatal(err)
	}
	if destinationPolicy.byID(initial.ID.Hex()) != nil {
		t.Fatal("archive original was resurrected at receiver")
	}
	stored := destinationPolicy.byID(latest.ID.Hex())
	if stored == nil || destinationPolicy.checkCalendarRead(*stored) != nil {
		t.Fatal("current walk not readable at receiver")
	}
	var invalid replicaBundle
	json.Unmarshal(bundle, &invalid)
	invalid.DependencyOnlyIDs = append(invalid.DependencyOnlyIDs, latest.ID.Hex())
	invalidData, _ := json.Marshal(invalid)
	if validateReplicaBundle(authCtx(t, authEvent), invalidData, scope, admin.Public()) == nil {
		t.Fatal("target occurrence could be hidden as a dependency")
	}
	if _, err := j.initialDependency(initial.ID.Hex(), cityB); err == nil {
		t.Fatal("cross-city archive was accepted")
	}
	original, err := j.initialDependency(initial.ID.Hex(), cityA)
	if err != nil || original == nil || original.ID != initial.ID {
		t.Fatal("exact signed dependency not retained")
	}
	deletion := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 10, Tags: nostr.Tags{{"e", initial.ID.Hex()}, {"i", cityA}, {"k", "31923"}}}
	deletion.Sign(creator)
	if err := db.SaveEvent(deletion); err != nil {
		t.Fatal(err)
	}
	if _, err := p.exportReplicaBundle(cityA, latest.ID.Hex()); err == nil {
		t.Fatal("canceled archived dependency was accepted")
	}
}
