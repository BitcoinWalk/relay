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
	anchors, bundle, owner, operator, root, rootContent := directoryTransportFixture(t)
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
	ownerUpdateContent := updateContent
	ownerUpdateContent.Sequence, ownerUpdateContent.PreviousEventID = 2, update.ID.Hex()
	ownerUpdate := directoryEvent(t, owner, ownerUpdateContent, 2)
	if err := writer.Publish(ctx, ownerUpdate); err != nil {
		t.Fatalf("current endpoint operator could not transport exact owner-signed successor: %v", err)
	}
}

func TestCityDirectoryTransportOperatorCanTransportButNotForgeOwnerEvent(t *testing.T) {
	anchors, bundle, owner, operator, root, rootContent := directoryTransportFixture(t)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "directory.db"), map[nostr.PubKey]bool{root.PubKey: true})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.DisableExpirationManager()
	defer db.Close()
	if _, err := configureCityDirectoryTransport(relay, db, anchors, bundle, cityA, "staging"); err != nil {
		t.Fatal(err)
	}

	content := rootContent
	content.Sequence, content.Action, content.PreviousEventID = 1, "update", root.ID.Hex()
	ownerUpdate := directoryEvent(t, owner, content, 1)
	if _, err := relay.AddEvent(khatru.ForceSetAuthed(context.Background(), nostr.GetPublicKey(operator)), ownerUpdate); err != nil {
		t.Fatalf("operator transport rejected exact owner event: %v", err)
	}

	forged := directoryEvent(t, operator, content, 2)
	if _, err := relay.AddEvent(khatru.ForceSetAuthed(context.Background(), nostr.GetPublicKey(operator)), forged); err == nil || !strings.Contains(err.Error(), "authorized next") {
		t.Fatalf("operator-forged owner update was not rejected: %v", err)
	}

	outsider := nostr.Generate()
	nextContent := content
	nextContent.Sequence, nextContent.PreviousEventID = 2, ownerUpdate.ID.Hex()
	nextOwnerUpdate := directoryEvent(t, owner, nextContent, 3)
	if _, err := relay.AddEvent(khatru.ForceSetAuthed(context.Background(), nostr.GetPublicKey(outsider)), nextOwnerUpdate); err == nil || !strings.Contains(err.Error(), "auth-required") {
		t.Fatalf("outsider transported an owner event: %v", err)
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

func multiCityDirectoryTransportFixture(t *testing.T) (string, string, map[string]nostr.SecretKey, map[string]nostr.Event, map[string]cityDirectoryContent) {
	t.Helper()
	dir := t.TempDir()
	anchorsPath := filepath.Join(dir, "anchors.json")
	bundlePath := filepath.Join(dir, "bundle.json")
	owners := map[string]nostr.SecretKey{}
	roots := map[string]nostr.Event{}
	contents := map[string]cityDirectoryContent{}
	anchors := make([]cityDirectoryAnchor, 0, 2)
	bundle := make([]nostr.Event, 0, 2)
	for index, cityID := range []string{cityA, cityB} {
		owner, recovery := nostr.Generate(), nostr.Generate()
		ownerPK, recoveryPK := nostr.GetPublicKey(owner), nostr.GetPublicKey(recovery)
		content := directoryContent(cityID, 0, "establish", "", ownerPK, nil, []nostr.PubKey{recoveryPK}, []cityPublicRelay{{URL: "wss://city" + string(rune('a'+index)) + ".example/", Role: "primary"}})
		root := directoryEvent(t, owner, content, index)
		owners[cityID], roots[cityID], contents[cityID] = owner, root, content
		anchors = append(anchors, cityDirectoryAnchor{CityID: cityID, RootEventID: root.ID.Hex(), InitialOwnerPubkey: ownerPK.Hex()})
		bundle = append(bundle, root)
	}
	writeDirectoryJSON(t, anchorsPath, cityDirectoryAnchorFile{Version: 1, Cities: anchors})
	writeDirectoryJSON(t, bundlePath, cityDirectoryMirrorFile{Version: 1, Events: bundle})
	return anchorsPath, bundlePath, owners, roots, contents
}

func TestMultiCityDirectoryTransportResolvesAndUpdatesCitiesIndependently(t *testing.T) {
	anchors, bundle, owners, roots, contents := multiCityDirectoryTransportFixture(t)
	dbPath := filepath.Join(t.TempDir(), "directory.db")
	relay, db, err := newRelay(dbPath, map[nostr.PubKey]bool{nostr.GetPublicKey(owners[cityA]): true, nostr.GetPublicKey(owners[cityB]): true})
	if err != nil {
		t.Fatal(err)
	}
	states, err := configureMultiCityDirectoryTransport(relay, db, anchors, bundle, nil, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[cityA].CurrentEventID != roots[cityA].ID.Hex() || states[cityB].CurrentEventID != roots[cityB].ID.Hex() {
		t.Fatalf("unexpected initial multi-city states: %#v", states)
	}

	content := contents[cityB]
	content.Sequence, content.Action, content.PreviousEventID = 1, "update", roots[cityB].ID.Hex()
	content.PublicRelays = []cityPublicRelay{{URL: "wss://cityb-new.example/", Role: "primary"}}
	update := directoryEvent(t, owners[cityB], content, 3)
	if _, err := relay.AddEvent(khatru.ForceSetAuthed(context.Background(), update.PubKey), update); err != nil {
		t.Fatal(err)
	}
	relay.DisableExpirationManager()
	db.Close()

	restarted, reopened, err := newRelay(dbPath, map[nostr.PubKey]bool{nostr.GetPublicKey(owners[cityA]): true, nostr.GetPublicKey(owners[cityB]): true})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.DisableExpirationManager()
	defer reopened.Close()
	states, err = configureMultiCityDirectoryTransport(restarted, reopened, anchors, bundle, nil, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if states[cityA].Sequence != 0 || states[cityA].ChainLength != 1 || states[cityB].Sequence != 1 || states[cityB].ChainLength != 2 || states[cityB].CurrentEventID != update.ID.Hex() {
		t.Fatalf("multi-city chains did not survive independently: %#v", states)
	}
}

func TestMultiCityDirectoryTransportRejectsUnanchoredWritesAndStorage(t *testing.T) {
	anchors, bundle, owners, _, _ := multiCityDirectoryTransportFixture(t)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "directory.db"), map[nostr.PubKey]bool{nostr.GetPublicKey(owners[cityA]): true})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.DisableExpirationManager()
	defer db.Close()
	if _, err := configureMultiCityDirectoryTransport(relay, db, anchors, bundle, nil, "staging"); err != nil {
		t.Fatal(err)
	}
	foreignOwner, recovery := nostr.Generate(), nostr.Generate()
	foreignContent := directoryContent("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", 0, "establish", "", nostr.GetPublicKey(foreignOwner), nil, []nostr.PubKey{nostr.GetPublicKey(recovery)}, []cityPublicRelay{{URL: "wss://foreign.example/", Role: "primary"}})
	foreign := directoryEvent(t, foreignOwner, foreignContent, 4)
	if _, err := relay.AddEvent(khatru.ForceSetAuthed(context.Background(), foreign.PubKey), foreign); err == nil || !strings.Contains(err.Error(), "trusted anchor") {
		t.Fatalf("unanchored event was accepted: %v", err)
	}
	if err := db.SaveEvent(foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAllCityDirectoryTransportEvents(db, map[string]cityDirectoryAnchor{cityA: {CityID: cityA}}); err == nil || !strings.Contains(err.Error(), "unanchored") {
		t.Fatalf("unanchored stored event was accepted: %v", err)
	}
}

func TestMultiCityDirectoryTransportRequiresBundleRootForEveryAnchor(t *testing.T) {
	anchorsPath, _, _, roots, _ := multiCityDirectoryTransportFixture(t)
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "incomplete.json")
	writeDirectoryJSON(t, bundlePath, cityDirectoryMirrorFile{Version: 1, Events: []nostr.Event{roots[cityA]}})
	relay, db, err := newRelay(filepath.Join(dir, "directory.db"), map[nostr.PubKey]bool{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.DisableExpirationManager()
	defer db.Close()
	if _, err := configureMultiCityDirectoryTransport(relay, db, anchorsPath, bundlePath, nil, "staging"); err == nil || !strings.Contains(err.Error(), cityB) {
		t.Fatalf("incomplete multi-city bundle was accepted: %v", err)
	}
}
