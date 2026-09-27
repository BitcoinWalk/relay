package main

import (
	"context"
	"errors"
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
		if err != nil || content.CityID != cityID {
			return nil, errors.New("city directory transport contains an invalid or foreign event")
		}
		events = append(events, event)
	}
	return events, nil
}

func configureCityDirectoryTransport(relay *khatru.Relay, db *boltdb.BoltBackend, anchorPath, bundlePath, cityID, mode string) (cityDirectoryState, error) {
	var empty cityDirectoryState
	if mode != "staging" && mode != "production" {
		return empty, errors.New("city directory transport mode must be staging or production")
	}
	anchors, err := loadCityDirectoryAnchors(anchorPath)
	if err != nil {
		return empty, err
	}
	anchor, ok := anchors[cityID]
	if !ok {
		return empty, errors.New("city directory transport has no trusted anchor")
	}
	bundle, err := loadCityDirectoryMirror(bundlePath)
	if err != nil {
		return empty, err
	}
	if _, err := resolveCityDirectory(bundle, anchor); err != nil {
		return empty, err
	}
	stored, err := loadCityDirectoryTransportEvents(db, cityID)
	if err != nil {
		return empty, err
	}
	combined := append(append([]nostr.Event(nil), stored...), bundle...)
	if _, err := resolveCityDirectory(combined, anchor); err != nil {
		return empty, err
	}
	storedIDs := make(map[nostr.ID]bool, len(stored))
	for _, event := range stored {
		storedIDs[event.ID] = true
	}
	for _, event := range bundle {
		if !storedIDs[event.ID] {
			if err := db.SaveEvent(event); err != nil && !errors.Is(err, eventstore.ErrDupEvent) {
				return empty, err
			}
		}
	}
	stored, err = loadCityDirectoryTransportEvents(db, cityID)
	if err != nil {
		return empty, err
	}
	state, err := resolveCityDirectory(stored, anchor)
	if err != nil {
		return empty, err
	}

	relay.Info.Name = "BitcoinWalk " + mode + " directory relay"
	relay.Info.Description = "Public read-only discovery with authenticated, authority-checked BitcoinWalk directory updates."
	relay.Info.Version = "bitcoinwalk-directory-transport-0.8.35"
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
		if err != nil || content.CityID != cityID {
			return true, "invalid: malformed or foreign city directory event"
		}
		currentEvents, err := loadCityDirectoryTransportEvents(db, cityID)
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
	return normalizeCityDirectoryState(state), nil
}
