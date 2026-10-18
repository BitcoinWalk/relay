package main

import (
	"encoding/json"
	"errors"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

// Operator-imported exact signed initial events are dependency history only.
// They are not source occurrences, and never receive an outbox/checkpoint.
var replicaInitialArchiveBucket = []byte("approved-initial-archive-v1")

func (j *replicaJournal) initialDependency(id, cityID string) (*nostr.Event, error) {
	var event *nostr.Event
	if entry, found, err := j.entry(id); err != nil {
		return nil, err
	} else if found {
		if entry.Action != "occurrence" || entry.CityID != cityID || entry.EventID != id {
			return nil, errors.New("invalid retained initial journal entry")
		}
		event = &entry.Event
	} else if err := j.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(replicaInitialArchiveBucket)
		if bucket == nil || bucket.Get([]byte(id)) == nil {
			return nil
		}
		var archived nostr.Event
		if err := json.Unmarshal(bucket.Get([]byte(id)), &archived); err != nil {
			return err
		}
		event = &archived
		return nil
	}); err != nil {
		return nil, err
	}
	if event == nil {
		return nil, nil
	}
	city, err := uniqueTag(*event, "i")
	marker, ok := exactCalendarTag(*event, "bitcoinwalk")
	if err != nil || city != cityID || !ok || marker != "initial-proposal-v1" || event.Kind != 31923 || event.ID.Hex() != id || !event.CheckID() || !event.VerifySignature() {
		return nil, errors.New("invalid signed initial dependency")
	}
	return event, nil
}
