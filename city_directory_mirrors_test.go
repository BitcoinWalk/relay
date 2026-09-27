package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
)

func writeDirectoryJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCityDirectoryMirrorConsensus(t *testing.T) {
	owner, operator, recovery := nostr.Generate(), nostr.Generate(), nostr.Generate()
	ownerPK, operatorPK, recoveryPK := nostr.GetPublicKey(owner), nostr.GetPublicKey(operator), nostr.GetPublicKey(recovery)
	rootContent := directoryContent(cityA, 0, "establish", "", ownerPK, []nostr.PubKey{operatorPK}, []nostr.PubKey{recoveryPK}, []cityPublicRelay{{URL: "wss://city.example/", Role: "primary"}})
	root := directoryEvent(t, owner, rootContent, 0)
	updateContent := rootContent
	updateContent.Sequence, updateContent.Action, updateContent.PreviousEventID = 1, "update", root.ID.Hex()
	updateContent.PublicRelays = []cityPublicRelay{{URL: "wss://city.example/", Role: "primary"}, {URL: "wss://mirror.example/", Role: "mirror"}}
	update := directoryEvent(t, operator, updateContent, 1)

	dir := t.TempDir()
	anchors := filepath.Join(dir, "anchors.json")
	one := filepath.Join(dir, "mirror-one.json")
	two := filepath.Join(dir, "mirror-two.json")
	writeDirectoryJSON(t, anchors, cityDirectoryAnchorFile{Version: 1, Cities: []cityDirectoryAnchor{{CityID: cityA, RootEventID: root.ID.Hex(), InitialOwnerPubkey: ownerPK.Hex()}}})
	writeDirectoryJSON(t, one, cityDirectoryMirrorFile{Version: 1, Events: []nostr.Event{root, update}})
	writeDirectoryJSON(t, two, cityDirectoryMirrorFile{Version: 1, Events: []nostr.Event{update, root, update}})

	result, err := auditCityDirectoryMirrors(anchors, []string{one, two}, cityA)
	if err != nil || result.MirrorCount != 2 || result.CurrentEventID != update.ID.Hex() || result.Sequence != 1 {
		t.Fatalf("matching independent mirrors rejected: %#v %v", result, err)
	}

	writeDirectoryJSON(t, two, cityDirectoryMirrorFile{Version: 1, Events: []nostr.Event{root}})
	if _, err := auditCityDirectoryMirrors(anchors, []string{one, two}, cityA); err == nil {
		t.Fatal("stale discovery mirror accepted")
	}
}

func TestCityDirectoryMirrorFilesAreStrictAndOwnerControlled(t *testing.T) {
	owner, recovery := nostr.Generate(), nostr.Generate()
	ownerPK, recoveryPK := nostr.GetPublicKey(owner), nostr.GetPublicKey(recovery)
	root := directoryEvent(t, owner, directoryContent(cityA, 0, "establish", "", ownerPK, nil, []nostr.PubKey{recoveryPK}, []cityPublicRelay{{URL: "wss://city.example/", Role: "primary"}}), 0)
	dir := t.TempDir()
	anchors := filepath.Join(dir, "anchors.json")
	one := filepath.Join(dir, "one.json")
	two := filepath.Join(dir, "two.json")
	writeDirectoryJSON(t, anchors, cityDirectoryAnchorFile{Version: 1, Cities: []cityDirectoryAnchor{{CityID: cityA, RootEventID: root.ID.Hex(), InitialOwnerPubkey: ownerPK.Hex()}}})
	writeDirectoryJSON(t, one, cityDirectoryMirrorFile{Version: 1, Events: []nostr.Event{root}})
	writeDirectoryJSON(t, two, cityDirectoryMirrorFile{Version: 1, Events: []nostr.Event{root}})

	if _, err := auditCityDirectoryMirrors(anchors, []string{one}, cityA); err == nil {
		t.Fatal("single discovery source accepted as independent consensus")
	}
	if err := os.Chmod(two, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := auditCityDirectoryMirrors(anchors, []string{one, two}, cityA); err == nil {
		t.Fatal("world-writable discovery mirror accepted")
	}
	if err := os.Chmod(two, 0600); err != nil {
		t.Fatal(err)
	}
	writeDirectoryJSON(t, two, map[string]any{"version": 1, "events": []nostr.Event{root}, "unexpected": true})
	if _, err := auditCityDirectoryMirrors(anchors, []string{one, two}, cityA); err == nil {
		t.Fatal("unknown mirror field accepted")
	}
}
