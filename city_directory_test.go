package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"fiatjaf.com/nostr"
)

func directoryEvent(t *testing.T, key nostr.SecretKey, content cityDirectoryContent, offset int) nostr.Event {
	t.Helper()
	tags := nostr.Tags{
		{"d", content.CityID},
		{"i", content.CityID},
		{"sequence", strconv.FormatUint(content.Sequence, 10)},
		{"action", content.Action},
		{"p", content.OwnerPubkey, "", "owner"},
	}
	if content.PreviousEventID != "" {
		tags = append(tags, nostr.Tag{"e", content.PreviousEventID, "", "directory-previous"})
	}
	for _, key := range content.OperatorPubkeys {
		tags = append(tags, nostr.Tag{"p", key, "", "operator"})
	}
	for _, key := range content.RecoveryPubkeys {
		tags = append(tags, nostr.Tag{"p", key, "", "recovery"})
	}
	for _, endpoint := range content.PublicRelays {
		tags = append(tags, nostr.Tag{"r", endpoint.URL, endpoint.Role})
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	event := nostr.Event{Kind: cityDirectoryKind, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Tags: tags, Content: string(data)}
	if err := event.Sign(key); err != nil {
		t.Fatal(err)
	}
	return event
}

func directoryContent(cityID string, sequence uint64, action, previous string, owner nostr.PubKey, operators, recovery []nostr.PubKey, relays []cityPublicRelay) cityDirectoryContent {
	operatorHex := make([]string, len(operators))
	for i, key := range operators {
		operatorHex[i] = key.Hex()
	}
	recoveryHex := make([]string, len(recovery))
	for i, key := range recovery {
		recoveryHex[i] = key.Hex()
	}
	slices.Sort(operatorHex)
	slices.Sort(recoveryHex)
	return cityDirectoryContent{Version: 1, CityID: cityID, Sequence: sequence, Action: action, PreviousEventID: previous, OwnerPubkey: owner.Hex(), OperatorPubkeys: operatorHex, RecoveryPubkeys: recoveryHex, PublicRelays: relays}
}

func TestCityDirectoryOwnerOperatorRotationAndRecovery(t *testing.T) {
	owner, nextOwner, recoveredOwner, operator, recovery := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	ownerPK, nextOwnerPK, recoveredOwnerPK := nostr.GetPublicKey(owner), nostr.GetPublicKey(nextOwner), nostr.GetPublicKey(recoveredOwner)
	operatorPK, recoveryPK := nostr.GetPublicKey(operator), nostr.GetPublicKey(recovery)
	rootContent := directoryContent(cityA, 0, "establish", "", ownerPK, []nostr.PubKey{operatorPK}, []nostr.PubKey{recoveryPK}, []cityPublicRelay{{URL: "wss://city.example/", Role: "primary"}, {URL: "wss://mirror.example/", Role: "mirror"}})
	root := directoryEvent(t, owner, rootContent, 0)
	anchor := cityDirectoryAnchor{CityID: cityA, RootEventID: root.ID.Hex(), InitialOwnerPubkey: ownerPK.Hex()}

	state, err := resolveCityDirectory([]nostr.Event{root}, anchor)
	if err != nil || state.CurrentEventID != root.ID.Hex() || state.OwnerPubkey != ownerPK.Hex() || state.ChainLength != 1 {
		t.Fatalf("valid root rejected: %#v %v", state, err)
	}

	operatorUpdateContent := rootContent
	operatorUpdateContent.Sequence = 1
	operatorUpdateContent.Action = "update"
	operatorUpdateContent.PreviousEventID = root.ID.Hex()
	operatorUpdateContent.PublicRelays = []cityPublicRelay{{URL: "wss://city.example/", Role: "primary"}, {URL: "wss://second-mirror.example/", Role: "mirror"}}
	operatorUpdate := directoryEvent(t, operator, operatorUpdateContent, 1)
	state, err = resolveCityDirectory([]nostr.Event{operatorUpdate, root, operatorUpdate}, anchor)
	if err != nil || state.CurrentEventID != operatorUpdate.ID.Hex() || len(state.PublicRelays) != 2 {
		t.Fatalf("operator endpoint update rejected: %#v %v", state, err)
	}

	rotateContent := operatorUpdateContent
	rotateContent.Sequence = 2
	rotateContent.Action = "rotate"
	rotateContent.PreviousEventID = operatorUpdate.ID.Hex()
	rotateContent.OwnerPubkey = nextOwnerPK.Hex()
	rotate := directoryEvent(t, owner, rotateContent, 2)
	state, err = resolveCityDirectory([]nostr.Event{root, operatorUpdate, rotate}, anchor)
	if err != nil || state.OwnerPubkey != nextOwnerPK.Hex() || state.CurrentEventID != rotate.ID.Hex() {
		t.Fatalf("owner rotation rejected: %#v %v", state, err)
	}

	oldOwnerUpdate := rotateContent
	oldOwnerUpdate.Sequence = 3
	oldOwnerUpdate.Action = "update"
	oldOwnerUpdate.PreviousEventID = rotate.ID.Hex()
	oldOwner := directoryEvent(t, owner, oldOwnerUpdate, 3)
	state, err = resolveCityDirectory([]nostr.Event{root, operatorUpdate, rotate, oldOwner}, anchor)
	if err != nil || state.CurrentEventID != rotate.ID.Hex() || state.OwnerPubkey != nextOwnerPK.Hex() {
		t.Fatalf("rotated owner affected directory state: %#v %v", state, err)
	}

	recoverContent := rotateContent
	recoverContent.Sequence = 3
	recoverContent.Action = "recover"
	recoverContent.PreviousEventID = rotate.ID.Hex()
	recoverContent.OwnerPubkey = recoveredOwnerPK.Hex()
	recoverContent.OperatorPubkeys = []string{}
	recoverEvent := directoryEvent(t, recovery, recoverContent, 4)
	state, err = resolveCityDirectory([]nostr.Event{recoverEvent, rotate, root, operatorUpdate}, anchor)
	if err != nil || state.OwnerPubkey != recoveredOwnerPK.Hex() || len(state.OperatorPubkeys) != 0 {
		t.Fatalf("declared recovery rejected: %#v %v", state, err)
	}
}

func TestCityDirectoryRejectsEscalationForksAndInvalidScope(t *testing.T) {
	owner, operator, recovery, attacker := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	ownerPK, operatorPK, recoveryPK := nostr.GetPublicKey(owner), nostr.GetPublicKey(operator), nostr.GetPublicKey(recovery)
	rootContent := directoryContent(cityA, 0, "establish", "", ownerPK, []nostr.PubKey{operatorPK}, []nostr.PubKey{recoveryPK}, []cityPublicRelay{{URL: "wss://city.example/", Role: "primary"}})
	root := directoryEvent(t, owner, rootContent, 0)
	anchor := cityDirectoryAnchor{CityID: cityA, RootEventID: root.ID.Hex(), InitialOwnerPubkey: ownerPK.Hex()}

	for name, mutate := range map[string]func(*cityDirectoryContent){
		"operator changes owner":     func(c *cityDirectoryContent) { c.OwnerPubkey = nostr.GetPublicKey(attacker).Hex() },
		"operator changes operators": func(c *cityDirectoryContent) { c.OperatorPubkeys = []string{nostr.GetPublicKey(attacker).Hex()} },
		"operator changes recovery":  func(c *cityDirectoryContent) { c.RecoveryPubkeys = []string{nostr.GetPublicKey(attacker).Hex()} },
	} {
		t.Run(name, func(t *testing.T) {
			content := rootContent
			content.Sequence, content.Action, content.PreviousEventID = 1, "update", root.ID.Hex()
			mutate(&content)
			event := directoryEvent(t, operator, content, 1)
			if _, err := resolveCityDirectory([]nostr.Event{root, event}, anchor); err == nil {
				t.Fatal("operator privilege escalation accepted")
			}
		})
	}

	firstContent := rootContent
	firstContent.Sequence, firstContent.Action, firstContent.PreviousEventID = 1, "update", root.ID.Hex()
	firstContent.PublicRelays = []cityPublicRelay{{URL: "wss://one.example/", Role: "primary"}}
	first := directoryEvent(t, owner, firstContent, 2)
	noiseContent := firstContent
	noiseContent.PublicRelays = []cityPublicRelay{{URL: "wss://noise.example/", Role: "primary"}}
	noise := directoryEvent(t, attacker, noiseContent, 2)
	state, err := resolveCityDirectory([]nostr.Event{root, noise, first}, anchor)
	if err != nil || state.CurrentEventID != first.ID.Hex() {
		t.Fatalf("unauthorized mirror noise blocked valid owner state: %#v %v", state, err)
	}
	secondContent := firstContent
	secondContent.PublicRelays = []cityPublicRelay{{URL: "wss://two.example/", Role: "primary"}}
	second := directoryEvent(t, owner, secondContent, 3)
	if _, err := resolveCityDirectory([]nostr.Event{root, first, second}, anchor); err == nil {
		t.Fatal("conflicting owner successors did not fail closed")
	}

	badEndpoint := rootContent
	badEndpoint.PublicRelays = []cityPublicRelay{{URL: "https://city.example/", Role: "primary"}}
	if _, err := decodeCityDirectoryEvent(directoryEvent(t, owner, badEndpoint, 4)); err == nil {
		t.Fatal("non-WSS public endpoint accepted")
	}
	badTags := root
	badTags.Tags = append(badTags.Tags, nostr.Tag{"r", "wss://injected.example/", "mirror"})
	badTags.Sign(owner)
	if _, err := decodeCityDirectoryEvent(badTags); err == nil {
		t.Fatal("content/tag mismatch accepted")
	}
	wrongAnchor := anchor
	wrongAnchor.RootEventID = fmt.Sprintf("%064d", 1)
	if _, err := resolveCityDirectory([]nostr.Event{root}, wrongAnchor); err == nil {
		t.Fatal("unanchored city root accepted")
	}
}

func TestMemphisCityDirectoryRootAcceptance(t *testing.T) {
	const raw = `{"content":"{\"version\":1,\"cityId\":\"be8514a4-9df0-4159-a517-71f65761cbbe\",\"sequence\":0,\"action\":\"establish\",\"previousEventId\":\"\",\"ownerPubkey\":\"4506e04e4b7079ce07e38e9875678a81ad33a456c696d708ef8e9a2d8c16ba04\",\"operatorPubkeys\":[],\"recoveryPubkeys\":[\"74d0c61ca913765c188dfc2265d27bcb401a1906ebd79c901dc977a69a2189f0\"],\"publicRelays\":[{\"url\":\"wss://replica-staging.bitcoinwalk.org/\",\"role\":\"primary\"}]}","created_at":1790530495,"id":"d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79","kind":30309,"pubkey":"4506e04e4b7079ce07e38e9875678a81ad33a456c696d708ef8e9a2d8c16ba04","sig":"065acfbf2e21980339f98b3a027d0e2187808dbb83b5fe7483803bd55fdb0950b97a7c1ac5a9ccc7a2a197836b7a74f36cadbb4f7512e993c9a937e88a5c4ee6","tags":[["d","be8514a4-9df0-4159-a517-71f65761cbbe"],["i","be8514a4-9df0-4159-a517-71f65761cbbe"],["sequence","0"],["action","establish"],["p","4506e04e4b7079ce07e38e9875678a81ad33a456c696d708ef8e9a2d8c16ba04","","owner"],["p","74d0c61ca913765c188dfc2265d27bcb401a1906ebd79c901dc977a69a2189f0","","recovery"],["r","wss://replica-staging.bitcoinwalk.org/","primary"]]}`
	var event nostr.Event
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	state, err := resolveCityDirectory([]nostr.Event{event}, cityDirectoryAnchor{
		CityID:             "be8514a4-9df0-4159-a517-71f65761cbbe",
		RootEventID:        "d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79",
		InitialOwnerPubkey: "4506e04e4b7079ce07e38e9875678a81ad33a456c696d708ef8e9a2d8c16ba04",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentEventID != event.ID.Hex() || state.Sequence != 0 || state.ChainLength != 1 || state.OwnerPubkey != event.PubKey.Hex() || !slices.Equal(state.RecoveryPubkeys, []string{"74d0c61ca913765c188dfc2265d27bcb401a1906ebd79c901dc977a69a2189f0"}) || !slices.Equal(state.PublicRelays, []cityPublicRelay{{URL: "wss://replica-staging.bitcoinwalk.org/", Role: "primary"}}) {
		t.Fatalf("unexpected Memphis root state: %#v", state)
	}
}
