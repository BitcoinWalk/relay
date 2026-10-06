package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"go.etcd.io/bbolt"
)

const replicaReplayConfirmation = "staging-idempotence-v1"
const replicaAlertRehearsalConfirmation = "staging-alert-transition-v1"
const replicaShadowBackfillConfirmation = "production-candidate-shadow-v1"
const replicaVisibleSeedConfirmation = "london-visible-seed-v1"

type replicaShadowBackfillResult struct {
	CityID               string `json:"cityId"`
	SourceDestination    string `json:"sourceDestination"`
	CandidateDestination string `json:"candidateDestination"`
	Delivered            int    `json:"delivered"`
	JournalMode          string `json:"journalMode"`
}

type replicaVisibleSeedResult struct {
	CityID      string `json:"cityId"`
	Destination string `json:"destination"`
	Delivered   int    `json:"delivered"`
	JournalMode string `json:"journalMode"`
}

type replicaPublicSnapshot struct {
	CityID   string `json:"cityId"`
	ReadOnly bool   `json:"readOnly"`
	Source   struct {
		OccurrenceIDs       []string `json:"occurrenceIds"`
		AllSignaturesValid  bool     `json:"allSignaturesValid"`
		PrivateWrapperCount int      `json:"privateWrapperCount"`
	} `json:"source"`
	Replica struct {
		OccurrenceIDs       []string `json:"occurrenceIds"`
		AllSignaturesValid  bool     `json:"allSignaturesValid"`
		PrivateWrapperCount int      `json:"privateWrapperCount"`
	} `json:"replica"`
	ExactEventIDs bool `json:"exactEventIds"`
}

func loadReplicaPublicSnapshot(path, cityID string) ([]string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("replica shadow public snapshot must be a non-writable regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snapshot replicaPublicSnapshot
	if json.Unmarshal(data, &snapshot) != nil || snapshot.CityID != cityID || !snapshot.ReadOnly || !snapshot.ExactEventIDs || !snapshot.Source.AllSignaturesValid || !snapshot.Replica.AllSignaturesValid || snapshot.Source.PrivateWrapperCount != 0 || snapshot.Replica.PrivateWrapperCount != 0 || len(snapshot.Source.OccurrenceIDs) == 0 || !slices.Equal(snapshot.Source.OccurrenceIDs, snapshot.Replica.OccurrenceIDs) {
		return nil, errors.New("replica shadow public snapshot is not an exact accepted baseline")
	}
	seen := make(map[string]struct{}, len(snapshot.Source.OccurrenceIDs))
	for _, eventID := range snapshot.Source.OccurrenceIDs {
		if len(eventID) != 64 {
			return nil, errors.New("replica shadow public snapshot contains an invalid event ID")
		}
		if _, duplicate := seen[eventID]; duplicate {
			return nil, errors.New("replica shadow public snapshot contains duplicate event IDs")
		}
		seen[eventID] = struct{}{}
	}
	return snapshot.Source.OccurrenceIDs, nil
}

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

func loadReplicaShadowEnvelopes(path, cityID, sourceDestination, candidateDestination string, publicIDs []string) ([]replicaDeliveryEnvelope, error) {
	if path == "" || !uuidPattern.MatchString(cityID) {
		return nil, errors.New("replica shadow backfill requires a journal and valid city")
	}
	source, sourceErr := normalizeReplicaDestination(sourceDestination)
	candidate, candidateErr := normalizeReplicaDestination(candidateDestination)
	if sourceErr != nil || candidateErr != nil || source != sourceDestination || candidate != candidateDestination || source == candidate {
		return nil, errors.New("replica shadow backfill requires distinct normalized destinations")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("replica shadow journal must be a non-writable regular file")
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open replica shadow journal read-only: %w", err)
	}
	defer db.Close()
	if len(publicIDs) == 0 {
		return nil, errors.New("replica shadow backfill requires an exact non-empty public snapshot")
	}
	public := make(map[string]struct{}, len(publicIDs))
	for _, eventID := range publicIDs {
		if len(eventID) != 64 {
			return nil, errors.New("replica shadow public event ID is invalid")
		}
		if _, duplicate := public[eventID]; duplicate {
			return nil, errors.New("replica shadow public event IDs are not unique")
		}
		public[eventID] = struct{}{}
	}
	envelopes := make([]replicaDeliveryEnvelope, 0, len(publicIDs))
	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(replicaOutboxBucket)
		if bucket == nil {
			return errors.New("replica shadow outbox is unavailable")
		}
		return bucket.ForEach(func(_, data []byte) error {
			var row replicaOutboxRow
			if err := json.Unmarshal(data, &row); err != nil {
				return err
			}
			if row.CityID != cityID {
				return nil
			}
			if row.Destination != source {
				return errors.New("replica shadow source destination mismatch")
			}
			switch row.Status {
			case "acknowledged":
				if err := validateAcknowledgedReplicaRow(&row, row.EventID); err != nil {
					return err
				}
				if _, visible := public[row.EventID]; !visible {
					return nil
				}
				envelope := *row.Envelope
				envelope.Bundle = append(json.RawMessage(nil), row.Envelope.Bundle...)
				envelope.Destination = candidate
				envelopes = append(envelopes, envelope)
			case "blocked", "canceled", "revoked", "stale-source":
				return nil
			default:
				return errors.New("replica shadow source outbox is not settled")
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if len(envelopes) != len(public) {
		return nil, errors.New("replica shadow journal does not contain every exact public occurrence")
	}
	slices.SortFunc(envelopes, func(a, b replicaDeliveryEnvelope) int {
		if a.SourceSequence < b.SourceSequence {
			return -1
		}
		if a.SourceSequence > b.SourceSequence {
			return 1
		}
		return 0
	})
	for index := 1; index < len(envelopes); index++ {
		if envelopes[index-1].SourceSequence >= envelopes[index].SourceSequence {
			return nil, errors.New("replica shadow source sequence is not strictly increasing")
		}
	}
	return envelopes, nil
}

func shadowBackfillReplica(ctx context.Context, journalPath, keyPath, cityID, sourceDestination, candidateDestination string, publicIDs []string, confirmation string, transport replicaTransport) (replicaShadowBackfillResult, error) {
	result := replicaShadowBackfillResult{CityID: cityID, SourceDestination: sourceDestination, CandidateDestination: candidateDestination, JournalMode: "read-only"}
	if confirmation != replicaShadowBackfillConfirmation {
		return result, errors.New("replica shadow backfill requires the exact production-candidate confirmation")
	}
	envelopes, err := loadReplicaShadowEnvelopes(journalPath, cityID, sourceDestination, candidateDestination, publicIDs)
	if err != nil {
		return result, err
	}
	if transport == nil {
		serviceKey, err := loadReplicaServiceKey(keyPath)
		if err != nil {
			return result, fmt.Errorf("load replica shadow key: %w", err)
		}
		transport = newReplicaWebSocketTransport(serviceKey)
	}
	for _, envelope := range envelopes {
		ack, err := transport(ctx, candidateDestination, envelope)
		if err != nil {
			return result, err
		}
		if !ack.Accepted || ack.EventID != envelope.OccurrenceID || ack.SourceSequence != envelope.SourceSequence {
			return result, errors.New("replica shadow backfill received an inexact acknowledgement")
		}
		result.Delivered++
	}
	return result, nil
}

func runReplicaShadowBackfill() error {
	cityID := os.Getenv("RELAY_REPLICA_SHADOW_CITY")
	publicIDs, err := loadReplicaPublicSnapshot(os.Getenv("RELAY_REPLICA_SHADOW_PUBLIC_SNAPSHOT"), cityID)
	if err != nil {
		return err
	}
	result, err := shadowBackfillReplica(
		context.Background(),
		os.Getenv("RELAY_REPLICA_JOURNAL"),
		os.Getenv("RELAY_REPLICA_DELIVERY_KEY_FILE"),
		cityID,
		os.Getenv("RELAY_REPLICA_SHADOW_SOURCE"),
		os.Getenv("RELAY_REPLICA_SHADOW_CANDIDATE"),
		publicIDs,
		os.Getenv("RELAY_REPLICA_SHADOW_CONFIRM"),
		nil,
	)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func validateVisibleSeedRow(row replicaOutboxRow, cityID, destination string) error {
	if row.CityID != cityID || row.Destination != destination || row.Control != nil || row.Envelope == nil {
		return errors.New("replica visible seed row has unexpected scope")
	}
	envelope := row.Envelope
	if row.Status != "pending" && row.Status != "retry" && row.Status != "acknowledged" {
		return errors.New("replica visible seed row is not deliverable")
	}
	if replicaEnvelopeAction(*envelope) != "occurrence" || envelope.Version != replicaEnvelopeVersion || envelope.OccurrenceID != row.EventID || envelope.Event.ID.Hex() != row.EventID || !envelope.Event.CheckID() || !envelope.Event.VerifySignature() || envelope.CityID != cityID || envelope.Destination != destination || envelope.SourceSequence == 0 || envelope.SourceSequence != row.SourceSequence || len(envelope.Bundle) == 0 {
		return errors.New("replica visible seed envelope is inconsistent")
	}
	return nil
}

func loadReplicaVisibleSeedEnvelopes(path, cityID, destination string, publicIDs []string) ([]replicaDeliveryEnvelope, error) {
	if path == "" || !uuidPattern.MatchString(cityID) {
		return nil, errors.New("replica visible seed requires a journal and valid city")
	}
	normalized, err := normalizeReplicaDestination(destination)
	if err != nil || normalized != destination {
		return nil, errors.New("replica visible seed requires a normalized destination")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("replica visible seed journal must be a protected regular file")
	}
	if len(publicIDs) == 0 {
		return nil, errors.New("replica visible seed requires an exact non-empty public snapshot")
	}
	public := make(map[string]struct{}, len(publicIDs))
	for _, eventID := range publicIDs {
		if len(eventID) != 64 {
			return nil, errors.New("replica visible seed public event ID is invalid")
		}
		if _, duplicate := public[eventID]; duplicate {
			return nil, errors.New("replica visible seed public event IDs are not unique")
		}
		public[eventID] = struct{}{}
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open replica visible seed journal read-only: %w", err)
	}
	defer db.Close()
	envelopes := make([]replicaDeliveryEnvelope, 0, len(publicIDs))
	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(replicaOutboxBucket)
		if bucket == nil {
			return errors.New("replica visible seed outbox is unavailable")
		}
		return bucket.ForEach(func(_, data []byte) error {
			var row replicaOutboxRow
			if err := json.Unmarshal(data, &row); err != nil {
				return err
			}
			if row.CityID != cityID {
				return nil
			}
			if row.Destination != destination {
				return errors.New("replica visible seed destination mismatch")
			}
			if _, visible := public[row.EventID]; !visible {
				return nil
			}
			if err := validateVisibleSeedRow(row, cityID, destination); err != nil {
				return err
			}
			envelope := *row.Envelope
			envelope.Bundle = append(json.RawMessage(nil), row.Envelope.Bundle...)
			envelopes = append(envelopes, envelope)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if len(envelopes) != len(public) {
		return nil, errors.New("replica visible seed journal does not contain every exact public occurrence")
	}
	slices.SortFunc(envelopes, func(a, b replicaDeliveryEnvelope) int {
		if a.SourceSequence < b.SourceSequence {
			return -1
		}
		if a.SourceSequence > b.SourceSequence {
			return 1
		}
		return 0
	})
	for index := 1; index < len(envelopes); index++ {
		if envelopes[index-1].SourceSequence >= envelopes[index].SourceSequence {
			return nil, errors.New("replica visible seed source sequence is not strictly increasing")
		}
	}
	return envelopes, nil
}

func visibleSeedReplica(ctx context.Context, journalPath, keyPath, cityID, destination string, publicIDs []string, confirmation string, transport replicaTransport) (replicaVisibleSeedResult, error) {
	result := replicaVisibleSeedResult{CityID: cityID, Destination: destination, JournalMode: "read-only"}
	if confirmation != replicaVisibleSeedConfirmation {
		return result, errors.New("replica visible seed requires the exact London confirmation")
	}
	envelopes, err := loadReplicaVisibleSeedEnvelopes(journalPath, cityID, destination, publicIDs)
	if err != nil {
		return result, err
	}
	if transport == nil {
		serviceKey, err := loadReplicaServiceKey(keyPath)
		if err != nil {
			return result, fmt.Errorf("load replica visible seed key: %w", err)
		}
		transport = newReplicaWebSocketTransport(serviceKey)
	}
	for _, envelope := range envelopes {
		ack, err := transport(ctx, destination, envelope)
		if err != nil {
			return result, err
		}
		if !ack.Accepted || ack.EventID != envelope.OccurrenceID || ack.SourceSequence != envelope.SourceSequence {
			return result, errors.New("replica visible seed received an inexact acknowledgement")
		}
		result.Delivered++
	}
	return result, nil
}

func runReplicaVisibleSeed() error {
	cityID := os.Getenv("RELAY_REPLICA_VISIBLE_SEED_CITY")
	publicIDs, err := loadReplicaPublicSnapshot(os.Getenv("RELAY_REPLICA_VISIBLE_SEED_PUBLIC_SNAPSHOT"), cityID)
	if err != nil {
		return err
	}
	result, err := visibleSeedReplica(
		context.Background(),
		os.Getenv("RELAY_REPLICA_JOURNAL"),
		os.Getenv("RELAY_REPLICA_DELIVERY_KEY_FILE"),
		cityID,
		os.Getenv("RELAY_REPLICA_VISIBLE_SEED_DESTINATION"),
		publicIDs,
		os.Getenv("RELAY_REPLICA_VISIBLE_SEED_CONFIRM"),
		nil,
	)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
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
