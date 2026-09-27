package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

func TestReplicaReconciliationAuditDetectsGapWithoutMutation(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))

	dir := t.TempDir()
	registryPath := filepath.Join(dir, "replicas.json")
	writeReplicaRegistry(t, registryPath, []replicaRegistryCity{{CityID: cityA, Destination: "wss://madeira.example/"}})
	registry, err := loadReplicaRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := openReplicaJournal(filepath.Join(dir, "journal.db"), registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if recovered, err := policy.recoverReplicaJournal(journal); err != nil || recovered != 0 {
		t.Fatalf("empty reconciliation failed: recovered=%d err=%v", recovered, err)
	}
	attachReplicaJournal(policy, journal)

	revision := draftEvent(t, creator, cityA, 0)
	accept(t, relay, revision)
	policy.mu.Lock()
	policy.replicaJournal = nil
	policy.mu.Unlock()
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, relay, approval)
	occurrence := replicatedOccurrence(t, creator, revision, approval, "2026-11-07", 3)
	accept(t, relay, occurrence)

	report, err := auditReplicaReconciliation(policy, journal)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != "degraded" || len(report.Cities) != 1 {
		t.Fatalf("gap was not reported: %#v", report)
	}
	city := report.Cities[0]
	if city.SourceRecords != 3 || city.JournalRecords != 0 || city.MissingRecords != 3 || city.OrphanedRecords != 0 {
		t.Fatalf("unexpected gap counts: %#v", city)
	}
	if _, found, err := journal.entry(occurrence.ID.Hex()); err != nil || found {
		t.Fatalf("read-only audit changed the journal: found=%v err=%v", found, err)
	}

	if recovered, err := policy.recoverReplicaJournal(journal); err != nil || recovered != 3 {
		t.Fatalf("recovery failed: recovered=%d err=%v", recovered, err)
	}
	report, err = auditReplicaReconciliation(policy, journal)
	if err != nil {
		t.Fatal(err)
	}
	city = report.Cities[0]
	if report.State != "healthy" || city.MissingRecords != 0 || city.OrphanedRecords != 0 || city.PublicOccurrences != 1 || city.UnsafeOutboxRows != 0 {
		t.Fatalf("recovered state did not audit healthy: %#v", report)
	}
}

func TestReplicaReconciliationAuditRecognizesSafeSuppression(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))

	dir := t.TempDir()
	registryPath := filepath.Join(dir, "replicas.json")
	writeReplicaRegistry(t, registryPath, []replicaRegistryCity{{CityID: cityA, Destination: "wss://madeira.example/"}})
	registry, err := loadReplicaRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := openReplicaJournal(filepath.Join(dir, "journal.db"), registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if _, err := policy.recoverReplicaJournal(journal); err != nil {
		t.Fatal(err)
	}
	attachReplicaJournal(policy, journal)

	revision := draftEvent(t, creator, cityA, 0)
	accept(t, relay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, relay, approval)
	occurrence := replicatedOccurrence(t, creator, revision, approval, "2026-11-14", 3)
	accept(t, relay, occurrence)
	tombstone := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 4, Tags: nostr.Tags{{"e", occurrence.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: audit suppression"}
	tombstone.Sign(creator)
	accept(t, relay, tombstone)

	report, err := auditReplicaReconciliation(policy, journal)
	if err != nil {
		t.Fatal(err)
	}
	city := report.Cities[0]
	if report.State != "healthy" || city.SuppressedOccurrences != 1 || city.PublicOccurrences != 0 || city.PendingRemovals != 0 || city.UnsafeOutboxRows != 0 {
		t.Fatalf("safe cancellation was not audited correctly: %#v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), occurrence.ID.Hex()) || strings.Contains(string(encoded), tombstone.ID.Hex()) {
		t.Fatalf("aggregate report leaked an event ID: %s", encoded)
	}

	if err := journal.db.Update(func(tx *bbolt.Tx) error {
		row, found, err := outboxRow(tx, occurrence.ID.Hex())
		if err != nil {
			return err
		}
		if !found {
			return errors.New("occurrence outbox row missing")
		}
		row.Status = "acknowledged"
		row.Control = nil
		return putOutboxRow(tx, *row)
	}); err != nil {
		t.Fatal(err)
	}
	report, err = auditReplicaReconciliation(policy, journal)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != "degraded" || report.Cities[0].UnsafeOutboxRows != 1 {
		t.Fatalf("unsafe resurrection state was not detected: %#v", report)
	}
}
