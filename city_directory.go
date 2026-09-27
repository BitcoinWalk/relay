package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"fiatjaf.com/nostr"
)

// cityDirectoryKind is a BitcoinWalk application-specific addressable event.
// It is not presented as a standardized NIP kind.
const cityDirectoryKind nostr.Kind = 30309

const cityDirectoryVersion = 1

type cityPublicRelay struct {
	URL  string `json:"url"`
	Role string `json:"role"`
}

type cityDirectoryContent struct {
	Version         int               `json:"version"`
	CityID          string            `json:"cityId"`
	Sequence        uint64            `json:"sequence"`
	Action          string            `json:"action"`
	PreviousEventID string            `json:"previousEventId"`
	OwnerPubkey     string            `json:"ownerPubkey"`
	OperatorPubkeys []string          `json:"operatorPubkeys"`
	RecoveryPubkeys []string          `json:"recoveryPubkeys"`
	PublicRelays    []cityPublicRelay `json:"publicRelays"`
}

type cityDirectoryAnchor struct {
	CityID             string `json:"cityId"`
	RootEventID        string `json:"rootEventId"`
	InitialOwnerPubkey string `json:"initialOwnerPubkey"`
}

type cityDirectoryState struct {
	Version         int               `json:"version"`
	CityID          string            `json:"cityId"`
	CurrentEventID  string            `json:"currentEventId"`
	Sequence        uint64            `json:"sequence"`
	OwnerPubkey     string            `json:"ownerPubkey"`
	OperatorPubkeys []string          `json:"operatorPubkeys"`
	RecoveryPubkeys []string          `json:"recoveryPubkeys"`
	PublicRelays    []cityPublicRelay `json:"publicRelays"`
	ChainLength     int               `json:"chainLength"`
}

func lowercaseHex32(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func validDirectoryKeys(owner string, operators, recovery []string) bool {
	if len(operators) > 20 || len(recovery) != 1 || !slices.IsSorted(operators) || !slices.IsSorted(recovery) {
		return false
	}
	seen := map[string]bool{}
	for _, key := range append(append([]string{owner}, operators...), recovery...) {
		parsed, err := nostr.PubKeyFromHex(key)
		if err != nil || parsed.Hex() != key || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func validPublicRelays(relays []cityPublicRelay) bool {
	if len(relays) < 1 || len(relays) > 8 || relays[0].Role != "primary" {
		return false
	}
	seen := map[string]bool{}
	previousMirror := ""
	for index, relay := range relays {
		normalized, err := normalizeReplicaDestination(relay.URL)
		if err != nil || normalized != relay.URL || seen[relay.URL] {
			return false
		}
		seen[relay.URL] = true
		if index == 0 {
			if relay.Role != "primary" {
				return false
			}
			continue
		}
		if relay.Role != "mirror" || previousMirror != "" && relay.URL <= previousMirror {
			return false
		}
		previousMirror = relay.URL
	}
	return true
}

func canonicalDirectoryTags(tags nostr.Tags) []string {
	result := make([]string, len(tags))
	for i, tag := range tags {
		result[i] = strings.Join(tag, "\x00")
	}
	slices.Sort(result)
	return result
}

func expectedDirectoryTags(content cityDirectoryContent) nostr.Tags {
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
	return tags
}

func decodeCityDirectoryEvent(event nostr.Event) (cityDirectoryContent, error) {
	var content cityDirectoryContent
	if event.Kind != cityDirectoryKind || len(event.Content) == 0 || len(event.Content) > 32*1024 || !event.CheckID() || !event.VerifySignature() {
		return content, errors.New("invalid city directory event or signature")
	}
	decoder := json.NewDecoder(strings.NewReader(event.Content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&content); err != nil {
		return content, errors.New("invalid city directory content")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return content, errors.New("invalid trailing city directory content")
	}
	if content.Version != cityDirectoryVersion || !uuidPattern.MatchString(content.CityID) || !slices.Contains([]string{"establish", "update", "rotate", "recover"}, content.Action) {
		return content, errors.New("invalid city directory scope")
	}
	if content.Sequence == 0 {
		if content.Action != "establish" || content.PreviousEventID != "" {
			return content, errors.New("invalid city directory root")
		}
	} else if content.Action == "establish" || !lowercaseHex32(content.PreviousEventID) {
		return content, errors.New("invalid city directory predecessor")
	}
	if !validDirectoryKeys(content.OwnerPubkey, content.OperatorPubkeys, content.RecoveryPubkeys) || !validPublicRelays(content.PublicRelays) {
		return content, errors.New("invalid city directory authority or endpoints")
	}
	if !slices.Equal(canonicalDirectoryTags(event.Tags), canonicalDirectoryTags(expectedDirectoryTags(content))) {
		return content, errors.New("city directory tags do not match signed content")
	}
	return content, nil
}

func equalDirectoryAuthority(a, b cityDirectoryContent) bool {
	return a.OwnerPubkey == b.OwnerPubkey && slices.Equal(a.OperatorPubkeys, b.OperatorPubkeys) && slices.Equal(a.RecoveryPubkeys, b.RecoveryPubkeys)
}

func equalPublicRelays(a, b []cityPublicRelay) bool { return slices.Equal(a, b) }

func validateDirectoryTransition(previous cityDirectoryContent, previousEvent nostr.Event, next cityDirectoryContent, nextEvent nostr.Event) error {
	if next.CityID != previous.CityID || next.Sequence != previous.Sequence+1 || next.PreviousEventID != previousEvent.ID.Hex() || nextEvent.CreatedAt <= previousEvent.CreatedAt {
		return errors.New("invalid city directory chain order")
	}
	signer := nextEvent.PubKey.Hex()
	switch next.Action {
	case "update":
		if next.OwnerPubkey != previous.OwnerPubkey {
			return errors.New("city directory update cannot rotate the owner")
		}
		if signer == previous.OwnerPubkey {
			return nil
		}
		if !slices.Contains(previous.OperatorPubkeys, signer) || !equalDirectoryAuthority(previous, next) {
			return errors.New("city directory operator may change endpoints only")
		}
		return nil
	case "rotate":
		if signer != previous.OwnerPubkey || next.OwnerPubkey == previous.OwnerPubkey || !slices.Equal(next.OperatorPubkeys, previous.OperatorPubkeys) || !slices.Equal(next.RecoveryPubkeys, previous.RecoveryPubkeys) || !equalPublicRelays(next.PublicRelays, previous.PublicRelays) {
			return errors.New("invalid city directory owner rotation")
		}
		return nil
	case "recover":
		if !slices.Contains(previous.RecoveryPubkeys, signer) || next.OwnerPubkey == previous.OwnerPubkey || len(next.OperatorPubkeys) != 0 || !slices.Equal(next.RecoveryPubkeys, previous.RecoveryPubkeys) || !equalPublicRelays(next.PublicRelays, previous.PublicRelays) {
			return errors.New("invalid city directory recovery")
		}
		return nil
	default:
		return errors.New("invalid city directory transition action")
	}
}

func resolveCityDirectory(events []nostr.Event, anchor cityDirectoryAnchor) (cityDirectoryState, error) {
	var state cityDirectoryState
	if !uuidPattern.MatchString(anchor.CityID) || !lowercaseHex32(anchor.RootEventID) {
		return state, errors.New("invalid city directory anchor")
	}
	initialOwner, err := nostr.PubKeyFromHex(anchor.InitialOwnerPubkey)
	if err != nil || initialOwner.Hex() != anchor.InitialOwnerPubkey {
		return state, errors.New("invalid city directory initial owner")
	}
	type record struct {
		event   nostr.Event
		content cityDirectoryContent
	}
	byID := map[string]record{}
	children := map[string][]record{}
	for _, event := range events {
		content, err := decodeCityDirectoryEvent(event)
		if err != nil {
			if event.ID.Hex() == anchor.RootEventID {
				return state, err
			}
			continue
		}
		if content.CityID != anchor.CityID {
			continue
		}
		id := event.ID.Hex()
		if _, exists := byID[id]; exists {
			continue
		}
		record := record{event: event, content: content}
		byID[id] = record
		if content.PreviousEventID != "" {
			children[content.PreviousEventID] = append(children[content.PreviousEventID], record)
		}
	}
	current, ok := byID[anchor.RootEventID]
	if !ok || current.content.Sequence != 0 || current.content.Action != "establish" || current.event.PubKey != initialOwner || current.content.OwnerPubkey != anchor.InitialOwnerPubkey {
		return state, errors.New("trusted city directory root unavailable")
	}
	chainLength := 1
	for {
		candidates := make([]record, 0)
		for _, candidate := range children[current.event.ID.Hex()] {
			signer := candidate.event.PubKey.Hex()
			authorized := candidate.content.Action == "update" && (signer == current.content.OwnerPubkey || slices.Contains(current.content.OperatorPubkeys, signer)) ||
				candidate.content.Action == "rotate" && signer == current.content.OwnerPubkey ||
				candidate.content.Action == "recover" && slices.Contains(current.content.RecoveryPubkeys, signer)
			if !authorized {
				continue
			}
			if err := validateDirectoryTransition(current.content, current.event, candidate.content, candidate.event); err != nil {
				return state, err
			}
			candidates = append(candidates, candidate)
		}
		if len(candidates) == 0 {
			break
		}
		if len(candidates) != 1 {
			return state, errors.New("conflicting city directory successors")
		}
		current = candidates[0]
		chainLength++
		if chainLength > 1000 {
			return state, errors.New("city directory chain limit exceeded")
		}
	}
	return cityDirectoryState{
		Version: cityDirectoryVersion, CityID: anchor.CityID, CurrentEventID: current.event.ID.Hex(), Sequence: current.content.Sequence,
		OwnerPubkey: current.content.OwnerPubkey, OperatorPubkeys: append([]string(nil), current.content.OperatorPubkeys...), RecoveryPubkeys: append([]string(nil), current.content.RecoveryPubkeys...), PublicRelays: append([]cityPublicRelay(nil), current.content.PublicRelays...), ChainLength: chainLength,
	}, nil
}
