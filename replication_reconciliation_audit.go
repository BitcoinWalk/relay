package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

type replicaReconciliationCity struct {
	CityID                string `json:"cityId"`
	Destination           string `json:"destination"`
	SourceRecords         int    `json:"sourceRecords"`
	JournalRecords        int    `json:"journalRecords"`
	MissingRecords        int    `json:"missingRecords"`
	OrphanedRecords       int    `json:"orphanedRecords"`
	PublicOccurrences     int    `json:"publicOccurrences"`
	SuppressedOccurrences int    `json:"suppressedOccurrences"`
	BlockedOccurrences    int    `json:"blockedOccurrences"`
	PendingRemovals       int    `json:"pendingRemovals"`
	UnsafeOutboxRows      int    `json:"unsafeOutboxRows"`
}

type replicaReconciliationReport struct {
	Version int                         `json:"version"`
	State   string                      `json:"state"`
	Cities  []replicaReconciliationCity `json:"cities"`
}

// auditReplicaReconciliation compares the authoritative source store with the
// separate replication journal and outbox without changing either database.
// The report is deliberately aggregate-only: it contains no event IDs,
// payloads, authors, delivery attempts or error text.
func auditReplicaReconciliation(policy *organizerPolicy, journal *replicaJournal) (replicaReconciliationReport, error) {
	if policy == nil || journal == nil || journal.registry == nil {
		return replicaReconciliationReport{}, errors.New("replica reconciliation audit unavailable")
	}
	policy.mu.Lock()
	defer policy.mu.Unlock()

	report := replicaReconciliationReport{Version: 1, State: "healthy"}
	byCity := make(map[string]*replicaReconciliationCity, len(journal.registry.destinations))
	for cityID, destination := range journal.registry.destinations {
		byCity[cityID] = &replicaReconciliationCity{CityID: cityID, Destination: destination}
	}

	type sourceRecord struct {
		event  nostr.Event
		action string
		cityID string
	}
	source := map[string]sourceRecord{}
	const scanLimit = 100000
	scanned := 0
	for event := range policy.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{5, 30302, 30304, eventModerationKind, 31923}}, scanLimit+1) {
		scanned++
		if scanned > scanLimit {
			return replicaReconciliationReport{}, errors.New("replica reconciliation audit scan limit exceeded")
		}
		action, cityID, relevant := replicaJournalAction(event)
		city := byCity[cityID]
		if !relevant || city == nil {
			continue
		}
		if !event.CheckID() || !event.VerifySignature() {
			return replicaReconciliationReport{}, errors.New("invalid signed replication record in authoritative source")
		}
		source[event.ID.Hex()] = sourceRecord{event: event, action: action, cityID: cityID}
		city.SourceRecords++
	}

	journalEntries := map[string]replicaJournalEntry{}
	outbox := map[string]replicaOutboxRow{}
	err := journal.db.View(func(tx *bbolt.Tx) error {
		entries := tx.Bucket(replicaEntriesBucket)
		events := tx.Bucket(replicaEventsBucket)
		if err := entries.ForEach(func(key, data []byte) error {
			if len(key) != 8 {
				return errors.New("invalid replica journal sequence")
			}
			var entry replicaJournalEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				return err
			}
			city := byCity[entry.CityID]
			if city == nil {
				return errors.New("replica journal contains an unconfigured city")
			}
			city.JournalRecords++
			indexed := events.Get([]byte(entry.EventID))
			if !bytes.Equal(indexed, key) || entry.Sequence != bboltSequence(key) || entry.Destination != city.Destination || entry.EventID != entry.Event.ID.Hex() || !entry.Event.CheckID() || !entry.Event.VerifySignature() {
				city.OrphanedRecords++
			}
			journalEntries[entry.EventID] = entry
			return nil
		}); err != nil {
			return err
		}
		return tx.Bucket(replicaOutboxBucket).ForEach(func(_, data []byte) error {
			var row replicaOutboxRow
			if err := json.Unmarshal(data, &row); err != nil {
				return err
			}
			outbox[row.EventID] = row
			return nil
		})
	})
	if err != nil {
		return replicaReconciliationReport{}, err
	}

	for eventID, record := range source {
		city := byCity[record.cityID]
		entry, found := journalEntries[eventID]
		if !found {
			city.MissingRecords++
			continue
		}
		if entry.Action != record.action || entry.CityID != record.cityID || entry.Event.ID != record.event.ID {
			city.OrphanedRecords++
		}
		if record.action == "moderation" {
			row, rowFound := outbox[eventID]
			if !rowFound || row.CityID != record.cityID || row.Destination != city.Destination || row.Envelope == nil || replicaEnvelopeAction(*row.Envelope) != "moderation" {
				city.UnsafeOutboxRows++
				continue
			}
			switch row.Status {
			case "acknowledged", "suppressed":
			case "pending", "retry":
				city.PendingRemovals++
			default:
				city.UnsafeOutboxRows++
			}
			continue
		}
		if record.action != "occurrence" {
			continue
		}
		row, rowFound := outbox[eventID]
		public := policy.checkCalendarRead(record.event) == nil
		if public {
			city.PublicOccurrences++
			if !rowFound || row.CityID != record.cityID || row.Destination != city.Destination || replicationControlState(row.Status) || row.Status == "stale-source" {
				city.UnsafeOutboxRows++
			} else if row.Status == "blocked" {
				city.BlockedOccurrences++
			}
			continue
		}
		city.SuppressedOccurrences++
		if !rowFound || row.CityID != record.cityID || row.Destination != city.Destination {
			city.UnsafeOutboxRows++
			continue
		}
		switch row.Status {
		case "blocked", "canceled", "revoked", "stale-source":
		case "cancellation-required", "revocation-required":
			city.PendingRemovals++
		default:
			city.UnsafeOutboxRows++
		}
	}

	for eventID, row := range outbox {
		if _, found := journalEntries[eventID]; found {
			continue
		}
		if city := byCity[row.CityID]; city != nil {
			city.UnsafeOutboxRows++
		}
	}

	for _, city := range byCity {
		if city.MissingRecords > 0 || city.OrphanedRecords > 0 || city.BlockedOccurrences > 0 || city.PendingRemovals > 0 || city.UnsafeOutboxRows > 0 {
			report.State = "degraded"
		}
		report.Cities = append(report.Cities, *city)
	}
	slices.SortFunc(report.Cities, func(a, b replicaReconciliationCity) int {
		if a.CityID < b.CityID {
			return -1
		}
		if a.CityID > b.CityID {
			return 1
		}
		return 0
	})
	return report, nil
}

func bboltSequence(key []byte) uint64 {
	return uint64(key[0])<<56 | uint64(key[1])<<48 | uint64(key[2])<<40 | uint64(key[3])<<32 |
		uint64(key[4])<<24 | uint64(key[5])<<16 | uint64(key[6])<<8 | uint64(key[7])
}
