package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

// TestReplicaTransportTLSSeparateRelays is the repeatable pre-deployment
// rehearsal for BW-56. It uses distinct source and destination stores, a real
// TLS WebSocket listener, NIP-42 and the production journal/outbox path.
func TestReplicaTransportTLSSeparateRelays(t *testing.T) {
	admin, creator, service := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)

	destinationRelay, destinationDB, err := newRelay(filepath.Join(t.TempDir(), "destination", "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer destinationDB.Close()
	defer destinationRelay.DisableExpirationManager()
	destinationPolicy := unitOrganizerPolicy(destinationRelay, enableOrganizers(destinationRelay, destinationDB, adminPK))
	tlsServer := httptest.NewUnstartedServer(destinationRelay)
	destination := "wss://" + tlsServer.Listener.Addr().String() + "/"
	receiver, err := newReplicaReceiver(destinationPolicy, replicaScope{
		CityID:      cityA,
		ServiceKey:  nostr.GetPublicKey(service),
		Destination: destination,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := attachReplicaReceiverTransport(destinationRelay, receiver); err != nil {
		t.Fatal(err)
	}
	tlsServer.StartTLS()
	defer tlsServer.Close()

	sourceRelay, sourceDB, err := newRelay(filepath.Join(t.TempDir(), "source", "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	defer sourceRelay.DisableExpirationManager()
	sourcePolicy := unitOrganizerPolicy(sourceRelay, enableOrganizers(sourceRelay, sourceDB, adminPK))
	journal, err := openReplicaJournal(filepath.Join(t.TempDir(), "source-journal.db"), &replicaRegistry{
		destinations: map[string]string{cityA: destination},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if recovered, err := sourcePolicy.recoverReplicaJournal(journal); err != nil || recovered != 0 {
		t.Fatalf("empty source reconciliation failed: recovered=%d err=%v", recovered, err)
	}
	attachReplicaJournal(sourcePolicy, journal)

	revision := draftEvent(t, creator, cityA, 0)
	accept(t, sourceRelay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	grantEvent := workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1)
	accept(t, sourceRelay, grantEvent)
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, sourceRelay, approval)
	occurrence := replicatedOccurrence(t, creator, revision, approval, "2026-10-10", 3)
	accept(t, sourceRelay, occurrence)
	second := replicatedOccurrence(t, creator, revision, approval, "2026-10-17", 4)
	accept(t, sourceRelay, second)

	row, found, err := journal.outboxEntry(occurrence.ID.Hex())
	if err != nil || !found || row.Envelope == nil {
		t.Fatalf("source occurrence was not queued: %#v %v", row, err)
	}
	// The default production trust store must reject the rehearsal's private CA.
	if _, err := deliverReplicaEnvelopeWSS(t.Context(), destination, *row.Envelope, service, nil); err == nil {
		t.Fatal("untrusted TLS destination was accepted")
	}
	if destinationPolicy.byID(occurrence.ID.Hex()) != nil {
		t.Fatal("failed TLS validation changed destination state")
	}

	trustedTLS := tlsServer.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	connector := func(ctx context.Context, requested string, options nostr.RelayOptions) (*nostr.Relay, error) {
		if requested != destination {
			t.Fatalf("transport dialed unconfigured destination %q", requested)
		}
		// Mirror the trusted reverse-proxy headers used by the staging Caddy
		// configuration so Khatru binds NIP-42 to the external WSS URL.
		options.RequestHeader = http.Header{
			"X-Forwarded-Host":  {tlsServer.Listener.Addr().String()},
			"X-Forwarded-Proto": {"https"},
		}
		client := nostr.NewRelay(context.Background(), requested, options)
		if err := client.ConnectWithTLS(ctx, trustedTLS.Clone()); err != nil {
			return client, err
		}
		return client, nil
	}
	transport := func(ctx context.Context, requested string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		return deliverReplicaEnvelopeWSS(ctx, requested, envelope, service, connector)
	}
	delivered, err := sourcePolicy.deliverReplicaOutbox(t.Context(), journal, time.Now(), transport)
	if err != nil || delivered != 2 {
		current, _, stateErr := journal.outboxEntry(occurrence.ID.Hex())
		secondState, _, secondStateErr := journal.outboxEntry(second.ID.Hex())
		t.Fatalf("TLS occurrence backfill failed: delivered=%d err=%v first=%#v firstErr=%v second=%#v secondErr=%v", delivered, err, current, stateErr, secondState, secondStateErr)
	}
	if stored := destinationPolicy.byID(occurrence.ID.Hex()); stored == nil || stored.ID != occurrence.ID {
		t.Fatal("destination did not retain the exact organizer-signed occurrence")
	}
	if stored := destinationPolicy.byID(second.ID.Hex()); stored == nil || stored.ID != second.ID {
		t.Fatal("destination did not retain the second exact organizer-signed occurrence")
	}
	for event := range destinationDB.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{replicaTransportKind}}, 1) {
		t.Fatalf("private transport wrapper entered public storage: %s", event.ID.Hex())
	}

	cancellation := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 5, Tags: nostr.Tags{{"e", occurrence.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: TLS rehearsal"}
	if err := cancellation.Sign(creator); err != nil {
		t.Fatal(err)
	}
	accept(t, sourceRelay, cancellation)
	delivered, err = sourcePolicy.deliverReplicaOutbox(t.Context(), journal, time.Now().Add(time.Second), transport)
	if err != nil || delivered != 1 {
		t.Fatalf("TLS cancellation delivery failed: delivered=%d err=%v", delivered, err)
	}
	if destinationPolicy.checkCalendarRead(occurrence) == nil {
		t.Fatal("destination still exposes the canceled occurrence")
	}
	if err := destinationPolicy.checkCalendarRead(second); err != nil {
		t.Fatalf("cancellation hid the other backfilled occurrence: %v", err)
	}
	if stored := destinationPolicy.byID(cancellation.ID.Hex()); stored == nil || stored.ID != cancellation.ID {
		t.Fatal("destination did not retain the exact organizer-signed cancellation")
	}

	if tlsServer.TLS == nil {
		t.Fatal("TLS rehearsal server was not configured")
	}
	if !strings.HasPrefix(tlsServer.URL, "https://") {
		t.Fatalf("unexpected rehearsal endpoint: %s", tlsServer.URL)
	}
}
