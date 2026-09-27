package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
)

func directoryTransportFixture(t *testing.T) (string, string, nostr.SecretKey, nostr.SecretKey, nostr.Event, cityDirectoryContent) {
	t.Helper()
	owner, operator, recovery := nostr.Generate(), nostr.Generate(), nostr.Generate()
	ownerPK, operatorPK, recoveryPK := nostr.GetPublicKey(owner), nostr.GetPublicKey(operator), nostr.GetPublicKey(recovery)
	content := directoryContent(cityA, 0, "establish", "", ownerPK, []nostr.PubKey{operatorPK}, []nostr.PubKey{recoveryPK}, []cityPublicRelay{{URL: "wss://city.example/", Role: "primary"}})
	root := directoryEvent(t, owner, content, 0)
	dir := t.TempDir()
	anchors := filepath.Join(dir, "anchors.json")
	bundle := filepath.Join(dir, "bundle.json")
	writeDirectoryJSON(t, anchors, cityDirectoryAnchorFile{Version: 1, Cities: []cityDirectoryAnchor{{CityID: cityA, RootEventID: root.ID.Hex(), InitialOwnerPubkey: ownerPK.Hex()}}})
	writeDirectoryJSON(t, bundle, cityDirectoryMirrorFile{Version: 1, Events: []nostr.Event{root}})
	return anchors, bundle, owner, operator, root, content
}

func TestCityDirectoryTransportPublicReadAndAuthenticatedWrite(t *testing.T) {
	anchors, bundle, _, operator, root, rootContent := directoryTransportFixture(t)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "directory.db"), map[nostr.PubKey]bool{root.PubKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configureCityDirectoryTransport(relay, db, anchors, bundle, cityA, "staging"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(relay)
	defer server.Close()
	defer relay.DisableExpirationManager()
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	reader, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := reader.Subscribe(ctx, nostr.Filter{IDs: []nostr.ID{root.ID}}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.Events:
		if event.ID != root.ID {
			t.Fatal("public reader received the wrong root")
		}
	case <-ctx.Done():
		t.Fatal("public reader could not retrieve the seeded root")
	}
	sub.Unsub()
	reader.Close()

	updateContent := rootContent
	updateContent.Sequence, updateContent.Action, updateContent.PreviousEventID = 1, "update", root.ID.Hex()
	update := directoryEvent(t, operator, updateContent, 1)
	writer, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Publish(ctx, update); err == nil || !strings.Contains(err.Error(), "auth-required") {
		t.Fatalf("unauthenticated WebSocket write accepted: %v", err)
	}
	if err := writer.Auth(ctx, func(_ context.Context, event *nostr.Event) error { return event.Sign(operator) }); err != nil {
		t.Fatal(err)
	}
	if err := writer.Publish(ctx, update); err != nil {
		t.Fatal(err)
	}
}

func TestCityDirectoryTransportSeedsAndRetainsAppendOnlyChain(t *testing.T) {
	anchors, bundle, _, operator, root, rootContent := directoryTransportFixture(t)
	dbPath := filepath.Join(t.TempDir(), "directory.db")
	relay, db, err := newRelay(dbPath, map[nostr.PubKey]bool{root.PubKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configureCityDirectoryTransport(relay, db, anchors, bundle, cityA, "staging"); err != nil {
		t.Fatal(err)
	}

	updateContent := rootContent
	updateContent.Sequence, updateContent.Action, updateContent.PreviousEventID = 1, "update", root.ID.Hex()
	updateContent.PublicRelays = []cityPublicRelay{{URL: "wss://new.example/", Role: "primary"}}
	update := directoryEvent(t, operator, updateContent, 1)
	authed := khatru.ForceSetAuthed(context.Background(), update.PubKey)
	if _, err := relay.AddEvent(authed, update); err != nil {
		t.Fatal(err)
	}
	relay.DisableExpirationManager()
	db.Close()

	restarted, reopened, err := newRelay(dbPath, map[nostr.PubKey]bool{root.PubKey: true})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.DisableExpirationManager()
	defer reopened.Close()
	state, err := configureCityDirectoryTransport(restarted, reopened, anchors, bundle, cityA, "staging")
	if err != nil || state.Sequence != 1 || state.CurrentEventID != update.ID.Hex() || state.ChainLength != 2 {
		t.Fatalf("append-only chain did not survive restart: %#v %v", state, err)
	}
	count := 0
	for range reopened.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{cityDirectoryKind}}, 10) {
		count++
	}
	if count != 2 {
		t.Fatalf("expected root and successor, got %d events", count)
	}
}

func TestCityDirectoryTransportRejectsWrongKindAuthAndAuthority(t *testing.T) {
	anchors, bundle, owner, _, root, rootContent := directoryTransportFixture(t)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "directory.db"), map[nostr.PubKey]bool{root.PubKey: true})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.DisableExpirationManager()
	defer db.Close()
	if _, err := configureCityDirectoryTransport(relay, db, anchors, bundle, cityA, "staging"); err != nil {
		t.Fatal(err)
	}

	updateContent := rootContent
	updateContent.Sequence, updateContent.Action, updateContent.PreviousEventID = 1, "update", root.ID.Hex()
	valid := directoryEvent(t, owner, updateContent, 1)
	if _, err := relay.AddEvent(context.Background(), valid); err == nil || !strings.Contains(err.Error(), "auth-required") {
		t.Fatalf("unauthenticated directory write accepted: %v", err)
	}

	attacker := nostr.Generate()
	unauthorized := directoryEvent(t, attacker, updateContent, 2)
	if _, err := relay.AddEvent(khatru.ForceSetAuthed(context.Background(), unauthorized.PubKey), unauthorized); err == nil || !strings.Contains(err.Error(), "authorized next") {
		t.Fatalf("unauthorized successor accepted: %v", err)
	}

	wrongKind := signedEvent(t, owner, 1, "not a directory event")
	if _, err := relay.AddEvent(khatru.ForceSetAuthed(context.Background(), wrongKind.PubKey), wrongKind); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("wrong kind accepted: %v", err)
	}
}

func TestCityDirectoryTransportSerializesCompetingSuccessors(t *testing.T) {
	anchors, bundle, owner, _, root, rootContent := directoryTransportFixture(t)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "directory.db"), map[nostr.PubKey]bool{root.PubKey: true})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.DisableExpirationManager()
	defer db.Close()
	if _, err := configureCityDirectoryTransport(relay, db, anchors, bundle, cityA, "staging"); err != nil {
		t.Fatal(err)
	}
	makeUpdate := func(endpoint string, offset int) nostr.Event {
		content := rootContent
		content.Sequence, content.Action, content.PreviousEventID = 1, "update", root.ID.Hex()
		content.PublicRelays = []cityPublicRelay{{URL: endpoint, Role: "primary"}}
		return directoryEvent(t, owner, content, offset)
	}
	candidates := []nostr.Event{makeUpdate("wss://one.example/", 1), makeUpdate("wss://two.example/", 2)}
	start := make(chan struct{})
	errorsByCandidate := make(chan error, len(candidates))
	var workers sync.WaitGroup
	for _, candidate := range candidates {
		candidate := candidate
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := relay.AddEvent(khatru.ForceSetAuthed(context.Background(), candidate.PubKey), candidate)
			errorsByCandidate <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errorsByCandidate)
	accepted := 0
	for err := range errorsByCandidate {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("expected exactly one competing successor, got %d", accepted)
	}
	events, err := loadCityDirectoryTransportEvents(db, cityA)
	if err != nil {
		t.Fatal(err)
	}
	state, err := resolveCityDirectory(events, cityDirectoryAnchor{CityID: cityA, RootEventID: root.ID.Hex(), InitialOwnerPubkey: root.PubKey.Hex()})
	if err != nil || state.Sequence != 1 || state.ChainLength != 2 {
		t.Fatalf("serialized chain is invalid: %#v %v", state, err)
	}
}
