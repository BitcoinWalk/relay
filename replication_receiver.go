package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/khatru"
	"go.etcd.io/bbolt"
)

const replicaReceiverLimit = 10000

var (
	replicaReceiverImportsBucket = []byte("bitcoinwalk-replica-imports")
	replicaReceiverHeadsBucket   = []byte("bitcoinwalk-replica-heads")
	replicaReceiverMetaBucket    = []byte("bitcoinwalk-replica-metadata")
	replicaReceiverConfigKey     = []byte("scope-sha256")
)

type replicaReceiver struct {
	policy    *organizerPolicy
	scope     replicaScope
	mu        sync.Mutex
	saveEvent func(nostr.Event) error
}

type replicaReceiverRecord struct {
	Envelope replicaDeliveryEnvelope `json:"envelope"`
	Status   string                  `json:"status"`
}

type replicaReceiverHead struct {
	SourceSequence uint64 `json:"sourceSequence"`
	EventID        string `json:"eventId"`
}

func newReplicaReceiver(policy *organizerPolicy, scope replicaScope) (*replicaReceiver, error) {
	if policy == nil || policy.db == nil || policy.db.DB == nil {
		return nil, errors.New("replica receiver requires destination storage")
	}
	if !uuidPattern.MatchString(scope.CityID) || scope.ServiceKey == (nostr.PubKey{}) || scope.ServiceKey == policy.admin {
		return nil, errors.New("restricted: invalid replica receiver scope")
	}
	destination, err := normalizeReplicaDestination(scope.Destination)
	if err != nil || destination != scope.Destination {
		return nil, errors.New("restricted: replica receiver destination must be normalized")
	}
	receiver := &replicaReceiver{
		policy: policy,
		scope:  scope,
		saveEvent: func(event nostr.Event) error {
			if event.Kind.IsReplaceable() || event.Kind.IsAddressable() {
				_, err := policy.db.ReplaceEvent(event)
				return err
			}
			return policy.db.SaveEvent(event)
		},
	}
	fingerprint := receiver.scopeFingerprint()
	if err := policy.db.DB.Update(func(tx *bbolt.Tx) error {
		for _, name := range [][]byte{replicaReceiverImportsBucket, replicaReceiverHeadsBucket, replicaReceiverMetaBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		meta := tx.Bucket(replicaReceiverMetaBucket)
		if stored := meta.Get(replicaReceiverConfigKey); stored != nil && !bytes.Equal(stored, fingerprint) {
			return errors.New("replica receiver scope changed; migration requires an explicit workflow")
		}
		if meta.Get(replicaReceiverConfigKey) == nil {
			return meta.Put(replicaReceiverConfigKey, fingerprint)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return receiver, nil
}

func (r *replicaReceiver) scopeFingerprint() []byte {
	sum := sha256.Sum256([]byte(r.scope.CityID + "\x00" + r.scope.Destination + "\x00" + r.scope.ServiceKey.Hex() + "\x00" + r.policy.admin.Hex()))
	return []byte(hex.EncodeToString(sum[:]))
}

func decodeReplicaBundle(data []byte) (*replicaBundle, error) {
	var bundle replicaBundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func validateReplicaEnvelope(ctx context.Context, envelope replicaDeliveryEnvelope, scope replicaScope, admin nostr.PubKey) (*replicaBundle, error) {
	if envelope.Version != replicaEnvelopeVersion || envelope.CityID != scope.CityID || envelope.Destination != scope.Destination || envelope.SourceSequence == 0 {
		return nil, errors.New("restricted: replica delivery scope or checkpoint mismatch")
	}
	if !envelope.Event.CheckID() || !envelope.Event.VerifySignature() {
		return nil, errors.New("invalid: replica delivery occurrence")
	}
	action := replicaEnvelopeAction(envelope)
	if action == "cancellation" {
		target, targetErr := uniqueTag(envelope.Event, "e")
		kind, kindErr := uniqueTag(envelope.Event, "k")
		city, cityErr := uniqueTag(envelope.Event, "i")
		tagsValid := true
		for _, tag := range envelope.Event.Tags {
			tagsValid = tagsValid && len(tag) == 2
		}
		if envelope.Event.Kind != 5 || targetErr != nil || kindErr != nil || cityErr != nil || target != envelope.OccurrenceID || kind != "31923" || city != scope.CityID || len(envelope.Event.Tags) != 3 || !tagsValid || !sizeOK(envelope.Event.Content, 1, 500) || len(envelope.Bundle) != 0 || envelope.CurrentApprovalID != "" || envelope.CurrentGrantID != "" {
			return nil, errors.New("invalid: replica cancellation envelope")
		}
		return nil, nil
	}
	if action == "revocation" {
		var decision cityDecision
		address, addressErr := uniqueTag(envelope.Event, "d")
		cityIndex, cityIndexErr := uniqueTag(envelope.Event, "i")
		status, statusOK := exactCalendarTag(envelope.Event, "status")
		revisionID, revisionErr := calendarReference(envelope.Event, "city-revision")
		if envelope.Event.Kind != 30304 || envelope.Event.PubKey != admin || json.Unmarshal([]byte(envelope.Event.Content), &decision) != nil || decision.CityID != scope.CityID || decision.Status != "revoked" || decision.RevisionID != revisionID || addressErr != nil || !workflowAddress(address, scope.CityID) || address != scope.CityID && (cityIndexErr != nil || cityIndex != scope.CityID) || revisionErr != nil || !statusOK || status != "revoked" || envelope.OccurrenceID != "" || len(envelope.Bundle) != 0 || envelope.CurrentApprovalID != "" || envelope.CurrentGrantID != "" {
			return nil, errors.New("invalid: replica revocation envelope")
		}
		return nil, nil
	}
	if action != "occurrence" || envelope.OccurrenceID == "" || envelope.OccurrenceID != envelope.Event.ID.Hex() {
		return nil, errors.New("invalid: replica delivery action or occurrence")
	}
	if err := validateReplicaBundle(ctx, envelope.Bundle, scope, admin); err != nil {
		return nil, err
	}
	bundle, err := decodeReplicaBundle(envelope.Bundle)
	if err != nil {
		return nil, err
	}
	if bundle.OccurrenceID != envelope.OccurrenceID {
		return nil, errors.New("invalid: delivery and bundle occurrences differ")
	}
	var target *nostr.Event
	grantID, approvalID := "", ""
	for i := range bundle.Events {
		event := &bundle.Events[i]
		switch event.Kind {
		case 30302:
			grantID = event.ID.Hex()
		case 30304:
			approvalID = event.ID.Hex()
		}
		if event.ID.Hex() == bundle.OccurrenceID {
			target = event
		}
	}
	targetJSON, targetErr := json.Marshal(target)
	eventJSON, eventErr := json.Marshal(envelope.Event)
	if target == nil || targetErr != nil || eventErr != nil || !bytes.Equal(targetJSON, eventJSON) || grantID != envelope.CurrentGrantID || approvalID != envelope.CurrentApprovalID {
		return nil, errors.New("invalid: replica delivery dependencies or checkpoint")
	}
	return bundle, nil
}

func (r *replicaReceiver) checkRevocationLocked(event nostr.Event) error {
	if existing := r.policy.byID(event.ID.Hex()); existing != nil && existing.ID == event.ID {
		return nil
	}
	var decision cityDecision
	if json.Unmarshal([]byte(event.Content), &decision) != nil || decision.Status != "revoked" || decision.CityID != r.scope.CityID {
		return errors.New("invalid: replica revocation decision")
	}
	current := r.policy.currentApproval(r.scope.CityID)
	if current == nil {
		return errors.New("restricted: replica revocation has no current destination approval")
	}
	var approved cityDecision
	if json.Unmarshal([]byte(current.Content), &approved) != nil || approved.RevisionID != decision.RevisionID || event.CreatedAt <= current.CreatedAt {
		return errors.New("restricted: replica revocation does not supersede the destination approval")
	}
	revision := r.policy.byID(decision.RevisionID)
	if revision == nil || revision.Kind != 30303 {
		return errors.New("invalid: replica revocation revision unavailable")
	}
	city, err := parseDraft(*revision)
	if err != nil || city.CityID != r.scope.CityID {
		return errors.New("invalid: replica revocation city/revision mismatch")
	}
	return nil
}

func receiverEnvelopeEqual(a, b replicaDeliveryEnvelope) bool {
	aJSON, aErr := json.Marshal(a)
	bJSON, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && bytes.Equal(aJSON, bJSON)
}

func receiverRecord(tx *bbolt.Tx, eventID string) (*replicaReceiverRecord, bool, error) {
	data := tx.Bucket(replicaReceiverImportsBucket).Get([]byte(eventID))
	if data == nil {
		return nil, false, nil
	}
	var record replicaReceiverRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, false, err
	}
	return &record, true, nil
}

func putReceiverRecord(tx *bbolt.Tx, eventID string, record replicaReceiverRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > 512*1024 {
		return errors.New("restricted: replica receiver record too large")
	}
	return tx.Bucket(replicaReceiverImportsBucket).Put([]byte(eventID), data)
}

func receiverHead(tx *bbolt.Tx, cityID string) (*replicaReceiverHead, error) {
	data := tx.Bucket(replicaReceiverHeadsBucket).Get([]byte(cityID))
	if data == nil {
		return nil, nil
	}
	var head replicaReceiverHead
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}
	return &head, nil
}

func (r *replicaReceiver) stage(envelope replicaDeliveryEnvelope) (*replicaReceiverRecord, error) {
	var staged *replicaReceiverRecord
	deliveryID := replicaEnvelopeAckID(envelope)
	err := r.policy.db.DB.Update(func(tx *bbolt.Tx) error {
		existing, found, err := receiverRecord(tx, deliveryID)
		if err != nil {
			return err
		}
		if found {
			if !receiverEnvelopeEqual(existing.Envelope, envelope) {
				return errors.New("restricted: conflicting replica replay")
			}
			staged = existing
			return nil
		}
		head, err := receiverHead(tx, envelope.CityID)
		if err != nil {
			return err
		}
		if head != nil {
			prior, found, err := receiverRecord(tx, head.EventID)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("replica receiver checkpoint is inconsistent")
			}
			if prior.Status != "installed" {
				return errors.New("restricted: previous replica import is incomplete")
			}
			if envelope.SourceSequence <= head.SourceSequence {
				return errors.New("restricted: stale or conflicting replica checkpoint")
			}
		}
		imports := tx.Bucket(replicaReceiverImportsBucket)
		if imports.Stats().KeyN >= replicaReceiverLimit {
			return errors.New("replica receiver capacity exceeded")
		}
		record := replicaReceiverRecord{Envelope: envelope, Status: "staged"}
		if err := putReceiverRecord(tx, deliveryID, record); err != nil {
			return err
		}
		headData, err := json.Marshal(replicaReceiverHead{SourceSequence: envelope.SourceSequence, EventID: deliveryID})
		if err != nil {
			return err
		}
		if err := tx.Bucket(replicaReceiverHeadsBucket).Put([]byte(envelope.CityID), headData); err != nil {
			return err
		}
		staged = &record
		return nil
	})
	return staged, err
}

func replicaInstallOrder(bundle *replicaBundle) []nostr.Event {
	events := append([]nostr.Event(nil), bundle.Events...)
	slices.SortStableFunc(events, func(a, b nostr.Event) int {
		rank := func(event nostr.Event) int {
			switch {
			case event.ID.Hex() == bundle.OccurrenceID:
				return 4
			case event.Kind == 30303:
				return 0
			case event.Kind == 30302:
				return 1
			case event.Kind == 31923:
				return 2
			case event.Kind == 30304:
				return 3
			default:
				return 5
			}
		}
		return rank(a) - rank(b)
	})
	return events
}

func (r *replicaReceiver) saveExact(event nostr.Event) error {
	err := r.saveEvent(event)
	if err == nil {
		return nil
	}
	if !errors.Is(err, eventstore.ErrDupEvent) {
		return err
	}
	stored := r.policy.byID(event.ID.Hex())
	if stored == nil || stored.ID != event.ID || !stored.CheckID() || !stored.VerifySignature() {
		return errors.New("replica receiver event-ID collision")
	}
	return nil
}

func (r *replicaReceiver) markInstalled(envelope replicaDeliveryEnvelope) error {
	return r.policy.db.DB.Update(func(tx *bbolt.Tx) error {
		deliveryID := replicaEnvelopeAckID(envelope)
		record, found, err := receiverRecord(tx, deliveryID)
		if err != nil {
			return err
		}
		if !found || !receiverEnvelopeEqual(record.Envelope, envelope) {
			return errors.New("replica receiver staged record changed")
		}
		record.Status = "installed"
		return putReceiverRecord(tx, deliveryID, *record)
	})
}

func (r *replicaReceiver) installedSupersedingOccurrence(envelope replicaDeliveryEnvelope) (*nostr.Event, error) {
	if replicaEnvelopeAction(envelope) != "occurrence" || !envelope.Event.Kind.IsAddressable() {
		return nil, nil
	}
	var superseding *nostr.Event
	err := r.policy.db.DB.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(replicaReceiverImportsBucket).ForEach(func(_, data []byte) error {
			var record replicaReceiverRecord
			if err := json.Unmarshal(data, &record); err != nil {
				return err
			}
			candidate := record.Envelope
			if record.Status != "installed" || replicaEnvelopeAction(candidate) != "occurrence" || candidate.SourceSequence <= envelope.SourceSequence || candidate.Event.Kind != envelope.Event.Kind || candidate.Event.PubKey != envelope.Event.PubKey || candidate.Event.Tags.GetD() != envelope.Event.Tags.GetD() || !nostr.IsOlder(envelope.Event, candidate.Event) {
				return nil
			}
			copy := candidate.Event
			superseding = &copy
			return nil
		})
	})
	return superseding, err
}

// receiveReplicaEnvelope is intentionally not exposed on the network. A later
// increment may call it from a bounded WSS handler after authenticating the
// configured service identity. The durable stage and city checkpoint commit in
// one transaction. Dependencies install before the occurrence, and no exact
// acknowledgement is returned until the occurrence is readable.
func (r *replicaReceiver) receiveReplicaEnvelope(ctx context.Context, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
	if !khatru.IsAuthed(ctx, r.scope.ServiceKey) {
		return replicaDeliveryAck{}, errors.New("auth-required: authenticate as configured replication service")
	}
	bundle, err := validateReplicaEnvelope(ctx, envelope, r.scope, r.policy.admin)
	if err != nil {
		return replicaDeliveryAck{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, err := r.stage(envelope)
	if err != nil {
		return replicaDeliveryAck{}, err
	}
	r.policy.mu.Lock()
	defer r.policy.mu.Unlock()
	if record.Status == "installed" {
		stored := r.policy.byID(replicaEnvelopeAckID(envelope))
		if stored != nil && stored.ID == envelope.Event.ID && stored.CheckID() && stored.VerifySignature() {
			return replicaDeliveryAck{EventID: replicaEnvelopeAckID(envelope), SourceSequence: envelope.SourceSequence, Accepted: true}, nil
		}
		superseding, err := r.installedSupersedingOccurrence(envelope)
		if err != nil {
			return replicaDeliveryAck{}, err
		}
		if superseding != nil {
			stored = r.policy.byID(superseding.ID.Hex())
			if stored != nil && stored.ID == superseding.ID && stored.CheckID() && stored.VerifySignature() {
				return replicaDeliveryAck{EventID: replicaEnvelopeAckID(envelope), SourceSequence: envelope.SourceSequence, Accepted: true}, nil
			}
		}
	}
	if replicaEnvelopeAction(envelope) == "cancellation" {
		if err := r.policy.checkCalendarDeletion(envelope.Event); err != nil {
			return replicaDeliveryAck{}, err
		}
		if err := r.saveExact(envelope.Event); err != nil {
			return replicaDeliveryAck{}, fmt.Errorf("replica cancellation install failed: %w", err)
		}
	} else if replicaEnvelopeAction(envelope) == "revocation" {
		if err := r.checkRevocationLocked(envelope.Event); err != nil {
			return replicaDeliveryAck{}, err
		}
		if err := r.saveExact(envelope.Event); err != nil {
			return replicaDeliveryAck{}, fmt.Errorf("replica revocation install failed: %w", err)
		}
	} else {
		for _, event := range replicaInstallOrder(bundle) {
			if event.ID.Hex() == bundle.OccurrenceID {
				// Re-check against destination state after dependencies are present and
				// while normal organizer/policy writes are excluded. Retained local
				// tombstones or a newer local revocation therefore fail closed.
				if err := r.policy.checkReplicaOccurrence(ctx, event, r.scope); err != nil {
					return replicaDeliveryAck{}, err
				}
			}
			if err := r.saveExact(event); err != nil {
				return replicaDeliveryAck{}, fmt.Errorf("replica install failed: %w", err)
			}
		}
	}
	if stored := r.policy.byID(replicaEnvelopeAckID(envelope)); stored == nil || stored.ID != envelope.Event.ID {
		return replicaDeliveryAck{}, errors.New("replica event unavailable after installation")
	}
	if err := r.markInstalled(envelope); err != nil {
		return replicaDeliveryAck{}, err
	}
	return replicaDeliveryAck{EventID: replicaEnvelopeAckID(envelope), SourceSequence: envelope.SourceSequence, Accepted: true}, nil
}

func (r *replicaReceiver) importRecord(eventID string) (*replicaReceiverRecord, bool, error) {
	var record *replicaReceiverRecord
	found := false
	err := r.policy.db.DB.View(func(tx *bbolt.Tx) error {
		var err error
		record, found, err = receiverRecord(tx, eventID)
		return err
	})
	return record, found, err
}
