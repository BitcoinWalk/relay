package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
)

const cityDirectoryTransportLimit = 1000

func loadCityDirectoryTransportEvents(db *boltdb.BoltBackend, cityID string) ([]nostr.Event, error) {
	events := make([]nostr.Event, 0)
	for event := range db.QueryEvents(nostr.Filter{}, cityDirectoryTransportLimit+1) {
		if len(events) == cityDirectoryTransportLimit {
			return nil, errors.New("city directory transport event limit exceeded")
		}
		content, err := decodeCityDirectoryEvent(event)
		if err != nil {
			return nil, errors.New("city directory transport contains an invalid event")
		}
		if content.CityID != cityID {
			continue
		}
		events = append(events, event)
	}
	return events, nil
}

func groupCityDirectoryEvents(events []nostr.Event, anchors map[string]cityDirectoryAnchor) (map[string][]nostr.Event, error) {
	grouped := make(map[string][]nostr.Event, len(anchors))
	for _, event := range events {
		content, err := decodeCityDirectoryEvent(event)
		if err != nil {
			return nil, errors.New("city directory transport contains an invalid event")
		}
		if _, ok := anchors[content.CityID]; !ok {
			return nil, errors.New("city directory transport contains an unanchored event")
		}
		grouped[content.CityID] = append(grouped[content.CityID], event)
	}
	return grouped, nil
}

func loadAllCityDirectoryTransportEvents(db *boltdb.BoltBackend, anchors map[string]cityDirectoryAnchor) (map[string][]nostr.Event, error) {
	events := make([]nostr.Event, 0)
	for event := range db.QueryEvents(nostr.Filter{}, cityDirectoryTransportLimit+1) {
		if len(events) == cityDirectoryTransportLimit {
			return nil, errors.New("city directory transport event limit exceeded")
		}
		events = append(events, event)
	}
	return groupCityDirectoryEvents(events, anchors)
}

func configureCityDirectoryTransport(relay *khatru.Relay, db *boltdb.BoltBackend, anchorPath, bundlePath, cityID, mode string) (cityDirectoryState, error) {
	var empty cityDirectoryState
	states, err := configureMultiCityDirectoryTransport(relay, db, anchorPath, bundlePath, []string{cityID}, mode)
	if err != nil {
		return empty, err
	}
	state, ok := states[cityID]
	if !ok {
		return empty, errors.New("city directory transport has no configured city state")
	}
	return state, nil
}

func configureMultiCityDirectoryTransport(relay *khatru.Relay, db *boltdb.BoltBackend, anchorPath, bundlePath string, selectedCityIDs []string, mode string) (map[string]cityDirectoryState, error) {
	if mode != "staging" && mode != "production" {
		return nil, errors.New("city directory transport mode must be staging or production")
	}
	anchors, err := loadCityDirectoryAnchors(anchorPath)
	if err != nil {
		return nil, err
	}
	if len(selectedCityIDs) > 0 {
		selected := make(map[string]cityDirectoryAnchor, len(selectedCityIDs))
		for _, cityID := range selectedCityIDs {
			anchor, ok := anchors[cityID]
			if !ok || cityID == "" {
				return nil, errors.New("city directory transport has no trusted anchor")
			}
			if _, duplicate := selected[cityID]; duplicate {
				return nil, errors.New("city directory transport has duplicate selected city")
			}
			selected[cityID] = anchor
		}
		anchors = selected
	}
	bundle, err := loadCityDirectoryMirror(bundlePath)
	if err != nil {
		return nil, err
	}
	bundleByCity, err := groupCityDirectoryEvents(bundle, anchors)
	if err != nil {
		return nil, err
	}
	for cityID, anchor := range anchors {
		if _, err := resolveCityDirectory(bundleByCity[cityID], anchor); err != nil {
			return nil, fmt.Errorf("validate city directory bundle for %s: %w", cityID, err)
		}
	}
	storedByCity, err := loadAllCityDirectoryTransportEvents(db, anchors)
	if err != nil {
		return nil, err
	}
	for cityID, anchor := range anchors {
		combined := append(append([]nostr.Event(nil), storedByCity[cityID]...), bundleByCity[cityID]...)
		if _, err := resolveCityDirectory(combined, anchor); err != nil {
			return nil, fmt.Errorf("validate stored city directory chain for %s: %w", cityID, err)
		}
	}
	storedIDs := make(map[nostr.ID]bool)
	for _, events := range storedByCity {
		for _, event := range events {
			storedIDs[event.ID] = true
		}
	}
	for _, event := range bundle {
		if !storedIDs[event.ID] {
			if err := db.SaveEvent(event); err != nil && !errors.Is(err, eventstore.ErrDupEvent) {
				return nil, err
			}
		}
	}
	storedByCity, err = loadAllCityDirectoryTransportEvents(db, anchors)
	if err != nil {
		return nil, err
	}
	states := make(map[string]cityDirectoryState, len(anchors))
	for cityID, anchor := range anchors {
		state, err := resolveCityDirectory(storedByCity[cityID], anchor)
		if err != nil {
			return nil, err
		}
		states[cityID] = normalizeCityDirectoryState(state)
	}

	relay.Info.Name = "BitcoinWalk " + mode + " directory relay"
	relay.Info.Description = "Public read-only discovery with authenticated, authority-checked BitcoinWalk directory updates."
	relay.Info.Version = "bitcoinwalk-directory-transport-0.8.79"
	relay.MaxAuthenticatedClients = 8
	var admission sync.Mutex
	relay.OnEvent = func(ctx context.Context, event nostr.Event) (bool, string) {
		admission.Lock()
		accepted := false
		defer func() {
			if !accepted {
				admission.Unlock()
			}
		}()
		if event.Kind != cityDirectoryKind {
			return true, "restricted: only BitcoinWalk city directory kind 30309 is accepted"
		}
		if !khatru.IsAuthed(ctx, event.PubKey) {
			return true, "auth-required: authenticate as the directory event author"
		}
		content, err := decodeCityDirectoryEvent(event)
		if err != nil {
			return true, "invalid: malformed city directory event"
		}
		anchor, ok := anchors[content.CityID]
		if !ok {
			return true, "restricted: city directory event has no trusted anchor"
		}
		currentEvents, err := loadCityDirectoryTransportEvents(db, content.CityID)
		if err != nil {
			return true, "error: directory storage validation failed"
		}
		current, err := resolveCityDirectory(currentEvents, anchor)
		if err != nil {
			return true, "error: current directory chain is invalid"
		}
		for _, storedEvent := range currentEvents {
			if storedEvent.ID == event.ID {
				accepted = true
				return false, ""
			}
		}
		candidate, err := resolveCityDirectory(append(currentEvents, event), anchor)
		if err != nil || candidate.CurrentEventID != event.ID.Hex() || candidate.Sequence != current.Sequence+1 || candidate.ChainLength != current.ChainLength+1 {
			return true, "restricted: event is not an authorized next directory chain record"
		}
		accepted = true
		return false, ""
	}
	relay.ReplaceEvent = func(_ context.Context, event nostr.Event) error {
		defer admission.Unlock()
		return db.SaveEvent(event)
	}
	return states, nil
}
