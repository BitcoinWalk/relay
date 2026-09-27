package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

const (
	replicaEnvelopeVersion = 1
	replicaOutboxLimit     = 10000
	replicaDeliveryBatch   = 20
)

var replicaOutboxBucket = []byte("outbox")

type replicaDeliveryEnvelope struct {
	Version           int             `json:"version"`
	Action            string          `json:"action"`
	CityID            string          `json:"cityId"`
	Destination       string          `json:"destination"`
	OccurrenceID      string          `json:"occurrenceId,omitempty"`
	SourceSequence    uint64          `json:"sourceSequence"`
	CurrentApprovalID string          `json:"currentApprovalId,omitempty"`
	CurrentGrantID    string          `json:"currentGrantId,omitempty"`
	Event             nostr.Event     `json:"event"`
	Bundle            json.RawMessage `json:"bundle,omitempty"`
}

type replicaOutboxRow struct {
	EventID        string                   `json:"eventId"`
	CityID         string                   `json:"cityId"`
	Destination    string                   `json:"destination"`
	SourceSequence uint64                   `json:"sourceSequence"`
	Status         string                   `json:"status"`
	Attempts       int                      `json:"attempts"`
	RetryAt        int64                    `json:"retryAt"`
	LastCode       string                   `json:"lastCode,omitempty"`
	Envelope       *replicaDeliveryEnvelope `json:"envelope,omitempty"`
	Control        *replicaDeliveryEnvelope `json:"control,omitempty"`
}

type replicaDeliveryAck struct {
	EventID        string
	SourceSequence uint64
	Accepted       bool
}

type replicaTransport func(context.Context, string, replicaDeliveryEnvelope) (replicaDeliveryAck, error)

type replicaOutboxSummary struct {
	CityID      string
	Destination string
	Statuses    map[string]int
}

func replicaEnvelope(entry replicaJournalEntry, checkpoint replicaCheckpoint, bundle []byte) replicaDeliveryEnvelope {
	return replicaDeliveryEnvelope{
		Version:           replicaEnvelopeVersion,
		Action:            "occurrence",
		CityID:            entry.CityID,
		Destination:       entry.Destination,
		OccurrenceID:      entry.EventID,
		SourceSequence:    checkpoint.SourceSequence,
		CurrentApprovalID: checkpoint.CurrentApprovalID,
		CurrentGrantID:    checkpoint.CurrentGrantID,
		Event:             entry.Event,
		Bundle:            append(json.RawMessage(nil), bundle...),
	}
}

func replicaControlEnvelope(entry replicaJournalEntry, action, occurrenceID string) replicaDeliveryEnvelope {
	return replicaDeliveryEnvelope{
		Version:        replicaEnvelopeVersion,
		Action:         action,
		CityID:         entry.CityID,
		Destination:    entry.Destination,
		OccurrenceID:   occurrenceID,
		SourceSequence: entry.Sequence,
		Event:          entry.Event,
	}
}

func replicaEnvelopeAction(envelope replicaDeliveryEnvelope) string {
	if envelope.Action == "" {
		return "occurrence"
	}
	return envelope.Action
}

func replicaEnvelopeAckID(envelope replicaDeliveryEnvelope) string {
	if replicaEnvelopeAction(envelope) == "occurrence" {
		return envelope.OccurrenceID
	}
	return envelope.Event.ID.Hex()
}

func outboxRow(tx *bbolt.Tx, eventID string) (*replicaOutboxRow, bool, error) {
	data := tx.Bucket(replicaOutboxBucket).Get([]byte(eventID))
	if data == nil {
		return nil, false, nil
	}
	var row replicaOutboxRow
	if err := json.Unmarshal(data, &row); err != nil {
		return nil, false, err
	}
	return &row, true, nil
}

func putOutboxRow(tx *bbolt.Tx, row replicaOutboxRow) error {
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if len(data) > 384*1024 {
		return errors.New("replica outbox row too large")
	}
	return tx.Bucket(replicaOutboxBucket).Put([]byte(row.EventID), data)
}

func ensureReplicaOutboxTx(tx *bbolt.Tx, entry replicaJournalEntry, envelope *replicaDeliveryEnvelope, blockedCode string) error {
	existing, found, err := outboxRow(tx, entry.EventID)
	if err != nil {
		return err
	}
	if found {
		if existing.CityID != entry.CityID || existing.Destination != entry.Destination {
			return errors.New("replica outbox scope mismatch")
		}
		if (existing.Status == "blocked" || existing.Status == "stale-source") && envelope != nil {
			existing.Status = "pending"
			existing.LastCode = ""
			existing.Envelope = envelope
			existing.SourceSequence = envelope.SourceSequence
			return putOutboxRow(tx, *existing)
		}
		return nil
	}
	bucket := tx.Bucket(replicaOutboxBucket)
	if bucket.Stats().KeyN >= replicaOutboxLimit {
		return errors.New("replica outbox capacity exceeded")
	}
	row := replicaOutboxRow{
		EventID:        entry.EventID,
		CityID:         entry.CityID,
		Destination:    entry.Destination,
		SourceSequence: entry.Sequence,
		Status:         "pending",
		Envelope:       envelope,
	}
	if envelope == nil {
		row.Status = "blocked"
		row.LastCode = blockedCode
	}
	return putOutboxRow(tx, row)
}

func suppressReplicaOutboxEventTx(tx *bbolt.Tx, eventID string, control replicaDeliveryEnvelope) error {
	row, found, err := outboxRow(tx, eventID)
	if err != nil || !found {
		return err
	}
	if row.Status == "revoked" || row.Status == "revocation-required" || row.Control != nil && replicaEnvelopeAction(*row.Control) == "revocation" {
		return nil
	}
	row.Control = &control
	row.SourceSequence = control.SourceSequence
	if row.Status == "acknowledged" {
		row.Status = "cancellation-required"
	} else if row.Status != "cancellation-required" {
		row.Status = "canceled"
	}
	row.RetryAt = 0
	row.LastCode = "canceled"
	return putOutboxRow(tx, *row)
}

func queueReplicaRevocationTx(tx *bbolt.Tx, entry replicaJournalEntry) error {
	control := replicaControlEnvelope(entry, "revocation", "")
	if existing, found, err := outboxRow(tx, entry.EventID); err != nil {
		return err
	} else if found {
		if existing.Envelope == nil || !receiverEnvelopeEqual(*existing.Envelope, control) {
			return errors.New("replica revocation outbox conflict")
		}
		return nil
	}
	if tx.Bucket(replicaOutboxBucket).Stats().KeyN >= replicaOutboxLimit {
		return errors.New("replica outbox capacity exceeded")
	}
	return putOutboxRow(tx, replicaOutboxRow{
		EventID:        entry.EventID,
		CityID:         entry.CityID,
		Destination:    entry.Destination,
		SourceSequence: entry.Sequence,
		Status:         "pending",
		Envelope:       &control,
	})
}

func suppressReplicaOutboxCityTx(tx *bbolt.Tx, cityID string, control replicaDeliveryEnvelope) (bool, error) {
	bucket := tx.Bucket(replicaOutboxBucket)
	rows := make([]replicaOutboxRow, 0)
	if err := bucket.ForEach(func(_, data []byte) error {
		var row replicaOutboxRow
		if err := json.Unmarshal(data, &row); err != nil {
			return err
		}
		if row.CityID == cityID {
			rows = append(rows, row)
		}
		return nil
	}); err != nil {
		return false, err
	}
	needsStandalone := false
	for _, row := range rows {
		if row.Envelope != nil && replicaEnvelopeAction(*row.Envelope) != "occurrence" {
			continue
		}
		if row.Status == "acknowledged" || row.Status == "cancellation-required" {
			needsStandalone = true
		}
		row.Control = &control
		if row.Status != "revocation-required" {
			row.Status = "revoked"
		}
		row.RetryAt = 0
		row.LastCode = "revoked"
		if err := putOutboxRow(tx, row); err != nil {
			return false, err
		}
	}
	return needsStandalone, nil
}

func (j *replicaJournal) outboxEntry(eventID string) (*replicaOutboxRow, bool, error) {
	var row *replicaOutboxRow
	found := false
	err := j.db.View(func(tx *bbolt.Tx) error {
		var err error
		row, found, err = outboxRow(tx, eventID)
		return err
	})
	return row, found, err
}

func (j *replicaJournal) dueOutbox(now int64) ([]replicaOutboxRow, error) {
	rows := make([]replicaOutboxRow, 0, replicaDeliveryBatch)
	err := j.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(replicaOutboxBucket).ForEach(func(_, data []byte) error {
			var row replicaOutboxRow
			if err := json.Unmarshal(data, &row); err != nil {
				return err
			}
			if (row.Status == "pending" || row.Status == "retry" || row.Status == "cancellation-required" || row.Status == "revocation-required") && row.RetryAt <= now {
				rows = append(rows, row)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b replicaOutboxRow) int {
		if a.SourceSequence < b.SourceSequence {
			return -1
		}
		if a.SourceSequence > b.SourceSequence {
			return 1
		}
		if a.EventID < b.EventID {
			return -1
		}
		if a.EventID > b.EventID {
			return 1
		}
		return 0
	})
	if len(rows) > replicaDeliveryBatch {
		rows = rows[:replicaDeliveryBatch]
	}
	return rows, nil
}

func (j *replicaJournal) updateOutbox(row replicaOutboxRow) error {
	return j.db.Update(func(tx *bbolt.Tx) error { return putOutboxRow(tx, row) })
}

func replicationControlState(status string) bool {
	switch status {
	case "canceled", "revoked", "cancellation-required", "revocation-required":
		return true
	default:
		return false
	}
}

func (j *replicaJournal) markOutboxStale(eventID string) error {
	return j.db.Update(func(tx *bbolt.Tx) error {
		row, found, err := outboxRow(tx, eventID)
		if err != nil || !found {
			return err
		}
		if replicationControlState(row.Status) {
			return nil
		}
		row.Status = "stale-source"
		row.LastCode = "source-ineligible"
		row.RetryAt = 0
		return putOutboxRow(tx, *row)
	})
}

func (j *replicaJournal) completeOutboxAttempt(attempt replicaOutboxRow, envelope replicaDeliveryEnvelope, now int64, accepted bool) (bool, error) {
	acknowledged := false
	action := replicaEnvelopeAction(envelope)
	err := j.db.Update(func(tx *bbolt.Tx) error {
		row, found, err := outboxRow(tx, attempt.EventID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("replica outbox row disappeared during delivery")
		}
		row.Attempts++
		if action != "occurrence" || !replicationControlState(row.Status) {
			row.SourceSequence = envelope.SourceSequence
		}
		if accepted {
			if action == "cancellation" {
				if row.Status == "revoked" || row.Status == "revocation-required" || row.Control != nil && replicaEnvelopeAction(*row.Control) == "revocation" {
					acknowledged = true
					return putOutboxRow(tx, *row)
				}
				row.Status = "canceled"
				row.LastCode = ""
				row.RetryAt = 0
				acknowledged = true
				return putOutboxRow(tx, *row)
			}
			if action == "revocation" {
				row.Status = "revoked"
				row.LastCode = ""
				row.RetryAt = 0
				acknowledged = true
				return putOutboxRow(tx, *row)
			}
			switch row.Status {
			case "canceled":
				if row.Control != nil {
					row.Status = "cancellation-required"
					row.LastCode = "canceled-during-delivery"
				}
			case "revoked":
				if row.Control != nil {
					row.Status = "revocation-required"
					row.LastCode = "revoked-during-delivery"
				}
			case "cancellation-required", "revocation-required":
				// Preserve the stronger ordered-removal requirement.
			default:
				row.Status = "acknowledged"
				row.LastCode = ""
			}
			row.RetryAt = 0
			acknowledged = true
			return putOutboxRow(tx, *row)
		}
		if action == "occurrence" && replicationControlState(row.Status) {
			return putOutboxRow(tx, *row)
		}
		if action == "occurrence" {
			row.Status = "retry"
		}
		row.LastCode = "delivery-failed"
		delay := int64(30 * (1 << min(row.Attempts-1, 7)))
		row.RetryAt = now + min(delay, int64(3600))
		return putOutboxRow(tx, *row)
	})
	return acknowledged, err
}

func (p *organizerPolicy) refreshedReplicaEnvelope(journal *replicaJournal, row replicaOutboxRow) (*replicaDeliveryEnvelope, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	checkpoint, err := p.freshReplicaCheckpointLocked(journal, row.CityID, row.EventID)
	if err != nil {
		return nil, err
	}
	bundle, err := p.exportReplicaBundleLocked(row.CityID, row.EventID)
	if err != nil {
		return nil, err
	}
	entry, found, err := journal.entry(row.EventID)
	if err != nil || !found {
		return nil, errors.New("replica journal entry unavailable")
	}
	envelope := replicaEnvelope(*entry, *checkpoint, bundle)
	return &envelope, nil
}

// deliverReplicaOutbox refreshes source eligibility immediately before handing
// a bounded envelope to the configured transport. Production uses the
// authenticated WSS transport; tests may inject a deterministic transport.
func (p *organizerPolicy) deliverReplicaOutbox(ctx context.Context, journal *replicaJournal, now time.Time, transport replicaTransport) (int, error) {
	if journal == nil || transport == nil || !journal.isReconciled() {
		return 0, errors.New("replica outbox is not ready")
	}
	journal.deliveryMu.Lock()
	defer journal.deliveryMu.Unlock()
	rows, err := journal.dueOutbox(now.Unix())
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, row := range rows {
		var envelope *replicaDeliveryEnvelope
		if row.Status == "cancellation-required" || row.Status == "revocation-required" {
			envelope = row.Control
			if envelope == nil {
				return delivered, errors.New("replica outbox control payload unavailable")
			}
		} else if row.Envelope != nil && replicaEnvelopeAction(*row.Envelope) != "occurrence" {
			envelope = row.Envelope
		} else {
			envelope, err = p.refreshedReplicaEnvelope(journal, row)
			if err != nil {
				if updateErr := journal.markOutboxStale(row.EventID); updateErr != nil {
					return delivered, updateErr
				}
				continue
			}
		}
		row.Envelope = envelope
		row.SourceSequence = envelope.SourceSequence
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ack, transportErr := transport(attemptCtx, row.Destination, *envelope)
		cancel()
		accepted := transportErr == nil && ack.Accepted && ack.EventID == replicaEnvelopeAckID(*envelope) && ack.SourceSequence == envelope.SourceSequence
		if acknowledged, err := journal.completeOutboxAttempt(row, *envelope, now.Unix(), accepted); err != nil {
			return delivered, err
		} else if acknowledged {
			delivered++
		}
	}
	return delivered, nil
}

// outboxSummary is deliberately content-free. A future BitcoinWalk Guide
// agent may consume this status to assist organizers, but never receives relay
// signing authority, private chat content, or destination-control capability.
func (j *replicaJournal) outboxSummary() ([]replicaOutboxSummary, error) {
	byCity := map[string]*replicaOutboxSummary{}
	err := j.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(replicaOutboxBucket).ForEach(func(_, data []byte) error {
			var row replicaOutboxRow
			if err := json.Unmarshal(data, &row); err != nil {
				return err
			}
			summary := byCity[row.CityID]
			if summary == nil {
				summary = &replicaOutboxSummary{CityID: row.CityID, Destination: row.Destination, Statuses: map[string]int{}}
				byCity[row.CityID] = summary
			}
			summary.Statuses[row.Status]++
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	result := make([]replicaOutboxSummary, 0, len(byCity))
	for _, summary := range byCity {
		result = append(result, *summary)
	}
	slices.SortFunc(result, func(a, b replicaOutboxSummary) int {
		if a.CityID < b.CityID {
			return -1
		}
		if a.CityID > b.CityID {
			return 1
		}
		return 0
	})
	return result, nil
}
