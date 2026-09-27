package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func writeReplicaRegistry(t *testing.T, path string, cities []replicaRegistryCity) {
	t.Helper()
	data, err := json.Marshal(replicaRegistryFile{Version: replicaRegistryVersion, Cities: cities})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func replicatedOccurrence(t *testing.T, key nostr.SecretKey, revision, approval nostr.Event, date string, offset int) nostr.Event {
	t.Helper()
	city, err := parseDraft(revision)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(48*time.Hour + time.Duration(offset)*time.Minute).Unix()
	event := nostr.Event{Kind: 31923, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Content: city.Description, Tags: nostr.Tags{
		{"d", city.CityID + ":" + date}, {"title", "BitcoinWalk " + city.CityName}, {"summary", "BitcoinWalk in " + city.CityName}, {"image", city.HeroImageURL},
		{"start", strconv.FormatInt(start, 10)}, {"D", strconv.FormatInt(start/86400, 10)}, {"location", city.MeetingPoint.Description}, {"location", "1,2"}, {"t", "bitcoinwalk"}, {"r", city.ChatURL},
		{"end", strconv.FormatInt(start+3600, 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", city.CityID}, {"bitcoinwalk", "occurrence-v1"},
		{"e", revision.ID.Hex(), "", "city-revision"}, {"e", approval.ID.Hex(), "", "city-approval"},
	}}
	event.Sign(key)
	return event
}

func TestReplicaRegistryIsStrictAndOperatorOwned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "replicas.json")
	writeReplicaRegistry(t, path, []replicaRegistryCity{{CityID: cityA, Destination: "wss://madeira.example"}})
	registry, err := loadReplicaRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if destination, ok := registry.destination(cityA); !ok || destination != "wss://madeira.example/" {
		t.Fatalf("unexpected normalized destination: %q %v", destination, ok)
	}

	for name, cities := range map[string][]replicaRegistryCity{
		"non-wss":        {{CityID: cityA, Destination: "https://madeira.example/"}},
		"path":           {{CityID: cityA, Destination: "wss://madeira.example/events"}},
		"credentials":    {{CityID: cityA, Destination: "wss://user:pass@madeira.example/"}},
		"duplicate city": {{CityID: cityA, Destination: "wss://one.example/"}, {CityID: cityA, Destination: "wss://two.example/"}},
		"shared target":  {{CityID: cityA, Destination: "wss://one.example/"}, {CityID: cityB, Destination: "wss://one.example/"}},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := filepath.Join(dir, name+".json")
			writeReplicaRegistry(t, candidate, cities)
			if _, err := loadReplicaRegistry(candidate); err == nil {
				t.Fatal("unsafe registry accepted")
			}
		})
	}

	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReplicaRegistry(path); err == nil {
		t.Fatal("group/world-writable registry accepted")
	}
}

func TestReplicaJournalOrdersAcceptedWritesAndRejectsStaleEvents(t *testing.T) {
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
	journalPath := filepath.Join(dir, "journal.db")
	journal, err := openReplicaJournal(journalPath, registry, func() time.Time { return time.Unix(1800000000, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if recovered, err := policy.recoverReplicaJournal(journal); err != nil || recovered != 0 {
		t.Fatalf("empty source reconciliation failed: recovered=%d err=%v", recovered, err)
	}
	attachReplicaJournal(policy, journal)

	revision := draftEvent(t, creator, cityA, 0)
	accept(t, relay, revision)
	if _, found, err := journal.entry(revision.ID.Hex()); err != nil || found {
		t.Fatal("private city revision entered the replication journal")
	}
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	grantEvent := workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1)
	accept(t, relay, grantEvent)
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, relay, approval)
	occurrence := replicatedOccurrence(t, creator, revision, approval, "2026-10-10", 3)
	accept(t, relay, occurrence)

	entry, found, err := journal.entry(occurrence.ID.Hex())
	if err != nil || !found {
		t.Fatalf("accepted occurrence missing from journal: %v", err)
	}
	if entry.Sequence != 3 || entry.Action != "occurrence" || !entry.EligibleAtAcceptance || entry.Event.ID != occurrence.ID || entry.CurrentApprovalID != approval.ID.Hex() || entry.CurrentGrantID != grantEvent.ID.Hex() {
		t.Fatalf("unexpected occurrence journal entry: %#v", entry)
	}
	checkpoint, err := policy.freshReplicaCheckpoint(journal, cityA, occurrence.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.SourceSequence != 3 || checkpoint.Destination != "wss://madeira.example/" || checkpoint.OccurrenceID != occurrence.ID.Hex() {
		t.Fatalf("unexpected source checkpoint: %#v", checkpoint)
	}
	outbox, found, err := journal.outboxEntry(occurrence.ID.Hex())
	if err != nil || !found || outbox.Status != "pending" || outbox.Envelope == nil || outbox.Envelope.Event.ID != occurrence.ID || len(outbox.Envelope.Bundle) == 0 {
		t.Fatalf("accepted occurrence was not atomically queued: %#v %v", outbox, err)
	}
	calls := 0
	delivered, err := policy.deliverReplicaOutbox(t.Context(), journal, time.Unix(1800000000, 0), func(_ context.Context, destination string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		calls++
		if destination != "wss://madeira.example/" || envelope.Event.ID != occurrence.ID || envelope.OccurrenceID != occurrence.ID.Hex() {
			t.Fatal("fake transport received wrong signed occurrence or destination")
		}
		return replicaDeliveryAck{EventID: envelope.OccurrenceID, SourceSequence: envelope.SourceSequence, Accepted: true}, nil
	})
	if err != nil || delivered != 1 || calls != 1 {
		t.Fatalf("fake delivery failed: delivered=%d calls=%d err=%v", delivered, calls, err)
	}
	outbox, found, err = journal.outboxEntry(occurrence.ID.Hex())
	if err != nil || !found || outbox.Status != "acknowledged" || outbox.Attempts != 1 {
		t.Fatalf("positive acknowledgement not persisted: %#v %v", outbox, err)
	}

	policy.mu.Lock()
	_, appended, err := journal.recordAcceptedLocked(policy, occurrence)
	policy.mu.Unlock()
	if err != nil || appended {
		t.Fatalf("duplicate acceptance changed journal: appended=%v err=%v", appended, err)
	}
	if head, err := journal.cityHead(cityA); err != nil || head != 3 {
		t.Fatalf("deduplication changed head: %d %v", head, err)
	}

	// A registry change must not silently redirect an already accepted event.
	journal.registry.destinations[cityA] = "wss://replacement.example/"
	if _, err := policy.freshReplicaCheckpoint(journal, cityA, occurrence.ID.Hex()); err == nil {
		t.Fatal("existing acceptance redirected to replacement destination")
	}
	journal.registry.destinations[cityA] = "wss://madeira.example/"

	cancellation := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 4, Tags: nostr.Tags{{"e", occurrence.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: organizer cancellation"}
	cancellation.Sign(creator)
	accept(t, relay, cancellation)
	if _, err := policy.freshReplicaCheckpoint(journal, cityA, occurrence.ID.Hex()); err == nil {
		t.Fatal("canceled occurrence retained a fresh source checkpoint")
	}
	cancelEntry, found, err := journal.entry(cancellation.ID.Hex())
	if err != nil || !found || cancelEntry.Sequence != 4 || cancelEntry.Action != "cancellation" {
		t.Fatalf("cancellation was not ordered after occurrence: %#v %v", cancelEntry, err)
	}
	outbox, found, err = journal.outboxEntry(occurrence.ID.Hex())
	if err != nil || !found || outbox.Status != "cancellation-required" {
		t.Fatalf("delivered occurrence did not require ordered cancellation: %#v %v", outbox, err)
	}
	if delivered, err = policy.deliverReplicaOutbox(t.Context(), journal, time.Unix(1800000100, 0), func(_ context.Context, _ string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		if replicaEnvelopeAction(envelope) != "cancellation" || envelope.Event.ID != cancellation.ID || envelope.OccurrenceID != occurrence.ID.Hex() {
			t.Fatal("fake transport received the wrong cancellation control")
		}
		return replicaDeliveryAck{EventID: envelope.Event.ID.Hex(), SourceSequence: envelope.SourceSequence, Accepted: true}, nil
	}); err != nil || delivered != 1 {
		t.Fatalf("ordered cancellation delivery failed: delivered=%d err=%v", delivered, err)
	}
	outbox, found, err = journal.outboxEntry(occurrence.ID.Hex())
	if err != nil || !found || outbox.Status != "canceled" {
		t.Fatalf("cancellation acknowledgement not persisted: %#v %v", outbox, err)
	}

	fresh := replicatedOccurrence(t, creator, revision, approval, "2026-10-17", 5)
	accept(t, relay, fresh)
	if checkpoint, err = policy.freshReplicaCheckpoint(journal, cityA, fresh.ID.Hex()); err != nil || checkpoint.SourceSequence != 5 {
		t.Fatalf("fresh occurrence checkpoint unavailable: %#v %v", checkpoint, err)
	}
	failedAt := time.Unix(1800000200, 0)
	if delivered, err = policy.deliverReplicaOutbox(t.Context(), journal, failedAt, func(_ context.Context, _ string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		return replicaDeliveryAck{EventID: "wrong", SourceSequence: envelope.SourceSequence, Accepted: true}, errors.New("do not retain remote diagnostics")
	}); err != nil || delivered != 0 {
		t.Fatalf("negative acknowledgement handling failed: delivered=%d err=%v", delivered, err)
	}
	outbox, found, err = journal.outboxEntry(fresh.ID.Hex())
	if err != nil || !found || outbox.Status != "retry" || outbox.RetryAt != failedAt.Unix()+30 || outbox.LastCode != "delivery-failed" {
		t.Fatalf("bounded sanitized retry not persisted: %#v %v", outbox, err)
	}
	if delivered, err = policy.deliverReplicaOutbox(t.Context(), journal, failedAt.Add(29*time.Second), func(context.Context, string, replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		t.Fatal("retry ran before due time")
		return replicaDeliveryAck{}, nil
	}); err != nil || delivered != 0 {
		t.Fatalf("early retry result: delivered=%d err=%v", delivered, err)
	}
	stable := replicatedOccurrence(t, creator, revision, approval, "2026-10-24", 6)
	accept(t, relay, stable)
	if delivered, err = policy.deliverReplicaOutbox(t.Context(), journal, failedAt.Add(29*time.Second), func(_ context.Context, _ string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		if envelope.OccurrenceID != stable.ID.Hex() || replicaEnvelopeAction(envelope) != "occurrence" {
			t.Fatal("fake transport received the wrong stable occurrence")
		}
		return replicaDeliveryAck{EventID: envelope.OccurrenceID, SourceSequence: envelope.SourceSequence, Accepted: true}, nil
	}); err != nil || delivered != 1 {
		t.Fatalf("stable occurrence delivery failed: delivered=%d err=%v", delivered, err)
	}
	stableCancellation := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 7, Tags: nostr.Tags{{"e", stable.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: overlaps city revocation"}
	stableCancellation.Sign(creator)
	accept(t, relay, stableCancellation)
	staleCancellationAttempt, found, err := journal.outboxEntry(stable.ID.Hex())
	if err != nil || !found || staleCancellationAttempt.Status != "cancellation-required" || staleCancellationAttempt.Control == nil {
		t.Fatalf("stable cancellation not ready for race test: %#v %v", staleCancellationAttempt, err)
	}
	staleCancellationEnvelope := *staleCancellationAttempt.Control
	revocation := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "revoked"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "revoked"}}, 8)
	if delivered, err = policy.deliverReplicaOutbox(t.Context(), journal, failedAt.Add(30*time.Second), func(_ context.Context, _ string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		// Exercise the delivery race: the source is revoked while the remote
		// acknowledgement is in flight. Completion must preserve the stronger
		// ordered-removal requirement rather than writing "acknowledged".
		accept(t, relay, revocation)
		return replicaDeliveryAck{EventID: envelope.OccurrenceID, SourceSequence: envelope.SourceSequence, Accepted: true}, nil
	}); err != nil || delivered != 1 {
		t.Fatalf("due retry did not succeed: delivered=%d err=%v", delivered, err)
	}
	if _, err := policy.freshReplicaCheckpoint(journal, cityA, fresh.ID.Hex()); err == nil {
		t.Fatal("city revocation did not invalidate source checkpoint")
	}
	if head, err := journal.cityHead(cityA); err != nil || head != 8 {
		t.Fatalf("revocation missing from ordered journal: %d %v", head, err)
	}
	outbox, found, err = journal.outboxEntry(fresh.ID.Hex())
	if err != nil || !found || outbox.Status != "revocation-required" {
		t.Fatalf("delivered occurrence did not require revocation propagation: %#v %v", outbox, err)
	}
	if acknowledged, err := journal.completeOutboxAttempt(*staleCancellationAttempt, staleCancellationEnvelope, failedAt.Add(30*time.Second).Unix(), true); err != nil || !acknowledged {
		t.Fatalf("stale cancellation completion failed: acknowledged=%v err=%v", acknowledged, err)
	}
	stableOutbox, found, err := journal.outboxEntry(stable.ID.Hex())
	if err != nil || !found || stableOutbox.Status != "revoked" || stableOutbox.Control == nil || replicaEnvelopeAction(*stableOutbox.Control) != "revocation" {
		t.Fatalf("stale cancellation overwrote stronger revocation: %#v %v", stableOutbox, err)
	}
	if delivered, err = policy.deliverReplicaOutbox(t.Context(), journal, failedAt.Add(31*time.Second), func(_ context.Context, _ string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		if replicaEnvelopeAction(envelope) != "revocation" || envelope.Event.ID != revocation.ID || envelope.OccurrenceID != "" {
			t.Fatal("fake transport received the wrong revocation control")
		}
		return replicaDeliveryAck{EventID: envelope.Event.ID.Hex(), SourceSequence: envelope.SourceSequence, Accepted: true}, nil
	}); err != nil || delivered != 2 {
		t.Fatalf("ordered revocation delivery failed: delivered=%d err=%v", delivered, err)
	}
	summaries, err := journal.outboxSummary()
	if err != nil || len(summaries) != 1 || summaries[0].Statuses["revoked"] != 4 {
		t.Fatalf("sanitized Guide/admin summary incorrect: %#v %v", summaries, err)
	}
	if err := journal.health(); err != nil {
		t.Fatalf("journal hook reported unhealthy: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openReplicaJournal(journalPath, registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if persisted, found, err := reopened.entry(revocation.ID.Hex()); err != nil || !found || persisted.Sequence != 8 {
		t.Fatalf("journal did not survive restart: %#v %v", persisted, err)
	}
}

func TestReplicaJournalRecoversCrashGapBeforeCheckpoints(t *testing.T) {
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
	journalPath := filepath.Join(dir, "journal.db")
	journal, err := openReplicaJournal(journalPath, registry, func() time.Time { return time.Unix(1800000000, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if recovered, err := policy.recoverReplicaJournal(journal); err != nil || recovered != 0 {
		t.Fatalf("initial reconciliation failed: recovered=%d err=%v", recovered, err)
	}
	attachReplicaJournal(policy, journal)

	revision := draftEvent(t, creator, cityA, 0)
	accept(t, relay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, relay, approval)

	// Simulate the narrow crash gap: source storage succeeds while the separate
	// journal is unavailable, then the process restarts before serving traffic.
	policy.mu.Lock()
	policy.replicaJournal = nil
	policy.mu.Unlock()
	kept := replicatedOccurrence(t, creator, revision, approval, "2026-10-24", 3)
	accept(t, relay, kept)
	canceled := replicatedOccurrence(t, creator, revision, approval, "2026-10-31", 4)
	accept(t, relay, canceled)
	tombstone := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 5, Tags: nostr.Tags{{"e", canceled.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: crash-gap test"}
	tombstone.Sign(creator)
	accept(t, relay, tombstone)
	if _, found, err := journal.entry(kept.ID.Hex()); err != nil || found {
		t.Fatal("simulated gap unexpectedly reached journal")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := openReplicaJournal(journalPath, registry, func() time.Time { return time.Unix(1800000100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err := policy.freshReplicaCheckpoint(restarted, cityA, kept.ID.Hex()); err == nil {
		t.Fatal("checkpoint available before startup reconciliation")
	}
	recovered, err := policy.recoverReplicaJournal(restarted)
	if err != nil || recovered != 3 {
		t.Fatalf("crash gap recovery failed: recovered=%d err=%v", recovered, err)
	}
	keptEntry, found, err := restarted.entry(kept.ID.Hex())
	if err != nil || !found || !keptEntry.Recovered || !keptEntry.EligibleAtAcceptance {
		t.Fatalf("public occurrence was not recovered safely: %#v %v", keptEntry, err)
	}
	canceledEntry, found, err := restarted.entry(canceled.ID.Hex())
	if err != nil || !found || !canceledEntry.Recovered || canceledEntry.EligibleAtAcceptance {
		t.Fatalf("canceled occurrence recovered as eligible: %#v %v", canceledEntry, err)
	}
	tombstoneEntry, found, err := restarted.entry(tombstone.ID.Hex())
	if err != nil || !found || !tombstoneEntry.Recovered || tombstoneEntry.Sequence <= canceledEntry.Sequence {
		t.Fatalf("tombstone was not recovered after occurrence: %#v %v", tombstoneEntry, err)
	}
	if _, err := policy.freshReplicaCheckpoint(restarted, cityA, kept.ID.Hex()); err != nil {
		t.Fatalf("current public occurrence unavailable after recovery: %v", err)
	}
	if _, err := policy.freshReplicaCheckpoint(restarted, cityA, canceled.ID.Hex()); err == nil {
		t.Fatal("recovery resurrected canceled occurrence")
	}
	keptOutbox, found, err := restarted.outboxEntry(kept.ID.Hex())
	if err != nil || !found || keptOutbox.Status != "pending" || keptOutbox.Envelope == nil {
		t.Fatalf("recovered public occurrence not queued: %#v %v", keptOutbox, err)
	}
	canceledOutbox, found, err := restarted.outboxEntry(canceled.ID.Hex())
	if err != nil || !found || canceledOutbox.Status != "canceled" {
		t.Fatalf("recovery queued canceled occurrence: %#v %v", canceledOutbox, err)
	}
	head, err := restarted.cityHead(cityA)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, err := policy.recoverReplicaJournal(restarted); err != nil || recovered != 0 {
		t.Fatalf("recovery is not idempotent: recovered=%d err=%v", recovered, err)
	}
	if after, err := restarted.cityHead(cityA); err != nil || after != head {
		t.Fatalf("idempotent recovery changed journal head: before=%d after=%d err=%v", head, after, err)
	}
}

func TestReplicaJournalRefusesImplicitRegistryMigration(t *testing.T) {
	dir := t.TempDir()
	first := &replicaRegistry{destinations: map[string]string{cityA: "wss://one.example/"}}
	path := filepath.Join(dir, "journal.db")
	journal, err := openReplicaJournal(path, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	changed := &replicaRegistry{destinations: map[string]string{cityA: "wss://two.example/"}}
	if reopened, err := openReplicaJournal(path, changed, nil); err == nil {
		reopened.Close()
		t.Fatal("journal accepted an implicit destination migration")
	}
}
