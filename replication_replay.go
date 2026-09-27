package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"go.etcd.io/bbolt"
)

const replicaReplayConfirmation = "staging-idempotence-v1"
const replicaAlertRehearsalConfirmation = "staging-alert-transition-v1"

func validateAcknowledgedReplicaRow(row *replicaOutboxRow, eventID string) error {
	if row == nil || row.EventID != eventID || row.Status != "acknowledged" || row.Envelope == nil || row.Control != nil {
		return errors.New("replica operation requires an acknowledged, unsuppressed occurrence")
	}
	envelope := row.Envelope
	if replicaEnvelopeAction(*envelope) != "occurrence" || envelope.Version != replicaEnvelopeVersion || envelope.OccurrenceID != eventID || envelope.Event.ID.Hex() != eventID || !envelope.Event.CheckID() || !envelope.Event.VerifySignature() || envelope.CityID != row.CityID || envelope.Destination != row.Destination || envelope.SourceSequence == 0 || envelope.SourceSequence != row.SourceSequence || len(envelope.Bundle) == 0 {
		return errors.New("replica operation outbox envelope is inconsistent")
	}
	normalized, err := normalizeReplicaDestination(envelope.Destination)
	if err != nil || normalized != envelope.Destination {
		return errors.New("replica operation destination is invalid")
	}
	return nil
}

// loadAcknowledgedReplicaEnvelope opens the source journal read-only. The
// source service must be stopped so this operator rehearsal never races or
// mutates normal outbox processing.
func loadAcknowledgedReplicaEnvelope(path, eventID string) (*replicaDeliveryEnvelope, error) {
	if path == "" || eventID == "" {
		return nil, errors.New("replica replay requires a journal and event ID")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("replica replay journal must be a non-writable regular file")
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open replica replay journal read-only: %w", err)
	}
	defer db.Close()
	var row replicaOutboxRow
	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(replicaOutboxBucket)
		if bucket == nil {
			return errors.New("replica replay outbox is unavailable")
		}
		data := bucket.Get([]byte(eventID))
		if data == nil {
			return errors.New("replica replay event is not in the outbox")
		}
		return json.Unmarshal(data, &row)
	})
	if err != nil {
		return nil, err
	}
	if err := validateAcknowledgedReplicaRow(&row, eventID); err != nil {
		return nil, err
	}
	envelope := row.Envelope
	copy := *envelope
	copy.Bundle = append(json.RawMessage(nil), envelope.Bundle...)
	return &copy, nil
}

// armAcknowledgedReplicaAlertRehearsal moves one exact, already-acknowledged
// occurrence back to pending so the normal durable worker can exercise a real
// retry -> acknowledged transition. The source service must be stopped: its
// bbolt lock makes this fail closed if normal processing is still running.
func armAcknowledgedReplicaAlertRehearsal(path, eventID, confirmation string) (*replicaOutboxRow, error) {
	if confirmation != replicaAlertRehearsalConfirmation {
		return nil, errors.New("replica alert rehearsal requires the exact staging confirmation")
	}
	if path == "" || eventID == "" {
		return nil, errors.New("replica alert rehearsal requires a journal and event ID")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("replica alert rehearsal journal must be a protected regular file")
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open replica alert rehearsal journal: %w", err)
	}
	defer db.Close()
	var armed replicaOutboxRow
	err = db.Update(func(tx *bbolt.Tx) error {
		row, found, err := outboxRow(tx, eventID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("replica alert rehearsal event is not in the outbox")
		}
		if err := validateAcknowledgedReplicaRow(row, eventID); err != nil {
			return err
		}
		row.Status = "pending"
		row.LastCode = ""
		row.RetryAt = 0
		if err := putOutboxRow(tx, *row); err != nil {
			return err
		}
		armed = *row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &armed, nil
}

func runReplicaAlertRehearsalArm() error {
	eventID := os.Getenv("RELAY_REPLICA_ALERT_REHEARSAL_EVENT")
	row, err := armAcknowledgedReplicaAlertRehearsal(
		os.Getenv("RELAY_REPLICA_JOURNAL"),
		eventID,
		os.Getenv("RELAY_REPLICA_ALERT_REHEARSAL_CONFIRM"),
	)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		EventID     string `json:"eventId"`
		CityID      string `json:"cityId"`
		Destination string `json:"destination"`
		State       string `json:"state"`
	}{row.EventID, row.CityID, row.Destination, row.Status})
}

func replayAcknowledgedReplica(ctx context.Context, journalPath, keyPath, eventID, confirmation string, transport replicaTransport) (replicaDeliveryAck, error) {
	if confirmation != replicaReplayConfirmation {
		return replicaDeliveryAck{}, errors.New("replica replay requires the exact staging confirmation")
	}
	envelope, err := loadAcknowledgedReplicaEnvelope(journalPath, eventID)
	if err != nil {
		return replicaDeliveryAck{}, err
	}
	if transport == nil {
		serviceKey, err := loadReplicaServiceKey(keyPath)
		if err != nil {
			return replicaDeliveryAck{}, fmt.Errorf("load replica replay key: %w", err)
		}
		transport = newReplicaWebSocketTransport(serviceKey)
	}
	ack, err := transport(ctx, envelope.Destination, *envelope)
	if err != nil {
		return replicaDeliveryAck{}, err
	}
	if !ack.Accepted || ack.EventID != eventID || ack.SourceSequence != envelope.SourceSequence {
		return replicaDeliveryAck{}, errors.New("replica replay received an inexact acknowledgement")
	}
	return ack, nil
}

func runReplicaReplay() error {
	eventID := os.Getenv("RELAY_REPLICA_REPLAY_EVENT")
	ack, err := replayAcknowledgedReplica(
		context.Background(),
		os.Getenv("RELAY_REPLICA_JOURNAL"),
		os.Getenv("RELAY_REPLICA_DELIVERY_KEY_FILE"),
		eventID,
		os.Getenv("RELAY_REPLICA_REPLAY_CONFIRM"),
		nil,
	)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		EventID        string `json:"eventId"`
		SourceSequence uint64 `json:"sourceSequence"`
		Accepted       bool   `json:"accepted"`
		JournalMode    string `json:"journalMode"`
	}{ack.EventID, ack.SourceSequence, ack.Accepted, "read-only"})
}
