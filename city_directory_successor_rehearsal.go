package main

import (
	"encoding/json"
	"errors"
	"os"
	"slices"

	"fiatjaf.com/nostr"
)

const cityDirectorySuccessorRehearsalConfirmation = "isolated-successors-v1"

type cityDirectoryRehearsalTransition struct {
	Name         string `json:"name"`
	Sequence     uint64 `json:"sequence"`
	EventID      string `json:"eventId"`
	SignerPubkey string `json:"signerPubkey"`
}

type cityDirectorySuccessorRehearsalReport struct {
	CityID                        string                             `json:"cityId"`
	RootEventID                   string                             `json:"rootEventId"`
	InitialOwnerPubkey            string                             `json:"initialOwnerPubkey"`
	Transitions                   []cityDirectoryRehearsalTransition `json:"transitions"`
	OperatorEscalationRejected    bool                               `json:"operatorEscalationRejected"`
	RotatedOwnerRejected          bool                               `json:"rotatedOwnerRejected"`
	ConflictingSuccessorsRejected bool                               `json:"conflictingSuccessorsRejected"`
	FinalOwnerPubkey              string                             `json:"finalOwnerPubkey"`
	FinalOperatorCount            int                                `json:"finalOperatorCount"`
	FinalSequence                 uint64                             `json:"finalSequence"`
	ChainLength                   int                                `json:"chainLength"`
}

func syntheticDirectoryContent(cityID string, sequence uint64, action, previous string, owner nostr.PubKey, operators []nostr.PubKey, recovery nostr.PubKey, relays []cityPublicRelay) cityDirectoryContent {
	operatorHex := make([]string, len(operators))
	for index, key := range operators {
		operatorHex[index] = key.Hex()
	}
	slices.Sort(operatorHex)
	return cityDirectoryContent{Version: 1, CityID: cityID, Sequence: sequence, Action: action, PreviousEventID: previous, OwnerPubkey: owner.Hex(), OperatorPubkeys: operatorHex, RecoveryPubkeys: []string{recovery.Hex()}, PublicRelays: relays}
}

func signSyntheticDirectoryEvent(secret nostr.SecretKey, content cityDirectoryContent, createdAt nostr.Timestamp) (nostr.Event, error) {
	encoded, err := json.Marshal(content)
	if err != nil {
		return nostr.Event{}, err
	}
	event := nostr.Event{Kind: cityDirectoryKind, CreatedAt: createdAt, Tags: expectedDirectoryTags(content), Content: string(encoded)}
	if err := event.Sign(secret); err != nil {
		return nostr.Event{}, err
	}
	return event, nil
}

func rehearseCityDirectorySuccessors() (cityDirectorySuccessorRehearsalReport, error) {
	var report cityDirectorySuccessorRehearsalReport
	const cityID = "8b6f3742-64b8-4d77-915a-a76b375fa06a"
	owner, operator, secondOperator, recovery, nextOwner, recoveredOwner, attacker := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	ownerPK, operatorPK, secondOperatorPK, recoveryPK := nostr.GetPublicKey(owner), nostr.GetPublicKey(operator), nostr.GetPublicKey(secondOperator), nostr.GetPublicKey(recovery)
	nextOwnerPK, recoveredOwnerPK, attackerPK := nostr.GetPublicKey(nextOwner), nostr.GetPublicKey(recoveredOwner), nostr.GetPublicKey(attacker)
	base := nostr.Timestamp(1800000000)
	rootContent := syntheticDirectoryContent(cityID, 0, "establish", "", ownerPK, []nostr.PubKey{operatorPK}, recoveryPK, []cityPublicRelay{{URL: "wss://synthetic-city.example/", Role: "primary"}})
	root, err := signSyntheticDirectoryEvent(owner, rootContent, base)
	if err != nil {
		return report, err
	}
	anchor := cityDirectoryAnchor{CityID: cityID, RootEventID: root.ID.Hex(), InitialOwnerPubkey: ownerPK.Hex()}

	ownerUpdateContent := syntheticDirectoryContent(cityID, 1, "update", root.ID.Hex(), ownerPK, []nostr.PubKey{operatorPK, secondOperatorPK}, recoveryPK, []cityPublicRelay{{URL: "wss://synthetic-city.example/", Role: "primary"}, {URL: "wss://synthetic-mirror.example/", Role: "mirror"}})
	ownerUpdate, err := signSyntheticDirectoryEvent(owner, ownerUpdateContent, base+1)
	if err != nil {
		return report, err
	}
	operatorUpdateContent := syntheticDirectoryContent(cityID, 2, "update", ownerUpdate.ID.Hex(), ownerPK, []nostr.PubKey{operatorPK, secondOperatorPK}, recoveryPK, []cityPublicRelay{{URL: "wss://operator-updated.example/", Role: "primary"}})
	operatorUpdate, err := signSyntheticDirectoryEvent(operator, operatorUpdateContent, base+2)
	if err != nil {
		return report, err
	}
	rotateContent := syntheticDirectoryContent(cityID, 3, "rotate", operatorUpdate.ID.Hex(), nextOwnerPK, []nostr.PubKey{operatorPK, secondOperatorPK}, recoveryPK, operatorUpdateContent.PublicRelays)
	rotate, err := signSyntheticDirectoryEvent(owner, rotateContent, base+3)
	if err != nil {
		return report, err
	}
	recoverContent := syntheticDirectoryContent(cityID, 4, "recover", rotate.ID.Hex(), recoveredOwnerPK, nil, recoveryPK, rotateContent.PublicRelays)
	recover, err := signSyntheticDirectoryEvent(recovery, recoverContent, base+4)
	if err != nil {
		return report, err
	}
	chain := []nostr.Event{root, ownerUpdate, operatorUpdate, rotate, recover}
	state, err := resolveCityDirectory(chain, anchor)
	if err != nil {
		return report, err
	}

	escalationContent := syntheticDirectoryContent(cityID, 1, "update", root.ID.Hex(), attackerPK, []nostr.PubKey{operatorPK}, recoveryPK, rootContent.PublicRelays)
	escalation, err := signSyntheticDirectoryEvent(operator, escalationContent, base+5)
	if err != nil {
		return report, err
	}
	_, escalationErr := resolveCityDirectory([]nostr.Event{root, escalation}, anchor)

	oldOwnerContent := syntheticDirectoryContent(cityID, 4, "update", rotate.ID.Hex(), nextOwnerPK, []nostr.PubKey{operatorPK, secondOperatorPK}, recoveryPK, []cityPublicRelay{{URL: "wss://stale-owner.example/", Role: "primary"}})
	oldOwnerEvent, err := signSyntheticDirectoryEvent(owner, oldOwnerContent, base+6)
	if err != nil {
		return report, err
	}
	oldOwnerState, oldOwnerErr := resolveCityDirectory([]nostr.Event{root, ownerUpdate, operatorUpdate, rotate, oldOwnerEvent}, anchor)

	conflictContent := syntheticDirectoryContent(cityID, 1, "update", root.ID.Hex(), ownerPK, []nostr.PubKey{operatorPK}, recoveryPK, []cityPublicRelay{{URL: "wss://conflict.example/", Role: "primary"}})
	conflict, err := signSyntheticDirectoryEvent(owner, conflictContent, base+7)
	if err != nil {
		return report, err
	}
	_, conflictErr := resolveCityDirectory([]nostr.Event{root, ownerUpdate, conflict}, anchor)

	if escalationErr == nil || oldOwnerErr != nil || oldOwnerState.CurrentEventID != rotate.ID.Hex() || conflictErr == nil {
		return report, errors.New("synthetic directory negative-policy rehearsal failed")
	}
	report = cityDirectorySuccessorRehearsalReport{
		CityID: cityID, RootEventID: root.ID.Hex(), InitialOwnerPubkey: ownerPK.Hex(),
		Transitions: []cityDirectoryRehearsalTransition{
			{Name: "owner-update", Sequence: 1, EventID: ownerUpdate.ID.Hex(), SignerPubkey: ownerPK.Hex()},
			{Name: "operator-update", Sequence: 2, EventID: operatorUpdate.ID.Hex(), SignerPubkey: operatorPK.Hex()},
			{Name: "rotate", Sequence: 3, EventID: rotate.ID.Hex(), SignerPubkey: ownerPK.Hex()},
			{Name: "recover", Sequence: 4, EventID: recover.ID.Hex(), SignerPubkey: recoveryPK.Hex()},
		},
		OperatorEscalationRejected: true, RotatedOwnerRejected: true, ConflictingSuccessorsRejected: true,
		FinalOwnerPubkey: state.OwnerPubkey, FinalOperatorCount: len(state.OperatorPubkeys), FinalSequence: state.Sequence, ChainLength: state.ChainLength,
	}
	return report, nil
}

func runCityDirectorySuccessorRehearsal() error {
	if os.Getenv("RELAY_CITY_DIRECTORY_SUCCESSOR_REHEARSAL") != cityDirectorySuccessorRehearsalConfirmation {
		return errors.New("city directory successor rehearsal requires exact isolated confirmation")
	}
	report, err := rehearseCityDirectorySuccessors()
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
