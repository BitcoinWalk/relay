package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

const replicaRegistryVersion = 1

var (
	replicaEntriesBucket = []byte("entries")
	replicaEventsBucket  = []byte("events")
	replicaHeadsBucket   = []byte("city-heads")
	replicaMetaBucket    = []byte("metadata")
	replicaRegistryKey   = []byte("registry-sha256")
	replicaReconciledKey = []byte("reconciled-at")
)

type replicaRegistryCity struct {
	CityID             string `json:"cityId"`
	Destination        string `json:"destination"`
	EntitlementEventID string `json:"entitlementEventId,omitempty"`
}

type replicaRegistryFile struct {
	Version int                   `json:"version"`
	Cities  []replicaRegistryCity `json:"cities"`
}

// replicaRegistry is operator-owned paid-city configuration. Relay addresses
// are never accepted from Nostr events or browser requests.
type replicaRegistry struct {
	destinations map[string]string
	entitlements map[string]string
}

func loadReplicaRegistry(path string) (*replicaRegistry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("replica registry must be a regular file not writable by group or others")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var input replicaRegistryFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid trailing replica registry data")
	}
	if input.Version != replicaRegistryVersion || len(input.Cities) == 0 || len(input.Cities) > 1000 {
		return nil, errors.New("invalid replica registry envelope")
	}
	registry := &replicaRegistry{destinations: make(map[string]string, len(input.Cities)), entitlements: make(map[string]string, len(input.Cities))}
	usedDestinations := map[string]bool{}
	for _, city := range input.Cities {
		if !uuidPattern.MatchString(city.CityID) {
			return nil, errors.New("invalid replica registry city")
		}
		destination, err := normalizeReplicaDestination(city.Destination)
		if err != nil {
			return nil, err
		}
		if _, exists := registry.destinations[city.CityID]; exists || usedDestinations[destination] {
			return nil, errors.New("duplicate replica registry city or destination")
		}
		registry.destinations[city.CityID] = destination
		if city.EntitlementEventID != "" {
			decoded, err := hex.DecodeString(city.EntitlementEventID)
			if err != nil || len(decoded) != 32 || strings.ToLower(city.EntitlementEventID) != city.EntitlementEventID {
				return nil, errors.New("invalid replica registry entitlement event")
			}
			registry.entitlements[city.CityID] = city.EntitlementEventID
		}
		usedDestinations[destination] = true
	}
	return registry, nil
}

func normalizeReplicaDestination(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "wss" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return "", errors.New("invalid replica destination")
	}
	u.Path = "/"
	return u.String(), nil
}

func (r *replicaRegistry) destination(cityID string) (string, bool) {
	if r == nil {
		return "", false
	}
	destination, ok := r.destinations[cityID]
	return destination, ok
}

func (r *replicaRegistry) entitlement(cityID string) (string, bool) {
	if r == nil {
		return "", false
	}
	eventID, ok := r.entitlements[cityID]
	return eventID, ok
}

func (r *replicaRegistry) fingerprint() string {
	lines := make([]string, 0, len(r.destinations))
	for cityID, destination := range r.destinations {
		line := cityID + "=" + destination
		if entitlementID, ok := r.entitlement(cityID); ok {
			line += "#" + entitlementID
		}
		lines = append(lines, line)
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

type replicaJournalEntry struct {
	Sequence             uint64      `json:"sequence"`
	Action               string      `json:"action"`
	CityID               string      `json:"cityId"`
	Destination          string      `json:"destination"`
	EventID              string      `json:"eventId"`
	Event                nostr.Event `json:"event"`
	AcceptedAt           int64       `json:"acceptedAt"`
	EligibleAtAcceptance bool        `json:"eligibleAtAcceptance"`
	CurrentApprovalID    string      `json:"currentApprovalId,omitempty"`
	CurrentGrantID       string      `json:"currentGrantId,omitempty"`
	Recovered            bool        `json:"recovered,omitempty"`
}

// replicaCheckpoint is a local source checkpoint, not a signed remote
// attestation. A later transport must fetch it from the trusted shared relay.
type replicaCheckpoint struct {
	CityID            string
	Destination       string
	OccurrenceID      string
	SourceSequence    uint64
	CurrentApprovalID string
	CurrentGrantID    string
}

type replicaJournal struct {
	db         *bbolt.DB
	registry   *replicaRegistry
	now        func() time.Time
	healthMu   sync.Mutex
	deliveryMu sync.Mutex
	lastErr    error
	reconciled bool
}

func openReplicaJournal(path string, registry *replicaRegistry, now func() time.Time) (*replicaJournal, error) {
	if path == "" || registry == nil || len(registry.destinations) == 0 {
		return nil, errors.New("replica journal requires a path and non-empty registry")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	journal := &replicaJournal{db: db, registry: registry, now: now}
	if journal.now == nil {
		journal.now = time.Now
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		for _, name := range [][]byte{replicaEntriesBucket, replicaEventsBucket, replicaHeadsBucket, replicaMetaBucket, replicaOutboxBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		meta := tx.Bucket(replicaMetaBucket)
		fingerprint := []byte(registry.fingerprint())
		stored := meta.Get(replicaRegistryKey)
		if stored != nil && !bytes.Equal(stored, fingerprint) {
			return errors.New("replica registry changed; endpoint migration requires an explicit workflow")
		}
		if stored == nil {
			if err := meta.Put(replicaRegistryKey, fingerprint); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.Close()
		return nil, err
	}
	return journal, nil
}

func (j *replicaJournal) Close() error { return j.db.Close() }

func (j *replicaJournal) setUnhealthy(err error) {
	if err == nil {
		return
	}
	j.healthMu.Lock()
	defer j.healthMu.Unlock()
	j.lastErr = err
	j.reconciled = false
}

func (j *replicaJournal) health() error {
	j.healthMu.Lock()
	defer j.healthMu.Unlock()
	return j.lastErr
}

func (j *replicaJournal) isReconciled() bool {
	j.healthMu.Lock()
	defer j.healthMu.Unlock()
	return j.reconciled && j.lastErr == nil
}

func (j *replicaJournal) beginReconciliation() error {
	j.healthMu.Lock()
	j.reconciled = false
	j.healthMu.Unlock()
	return j.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(replicaMetaBucket).Delete(replicaReconciledKey)
	})
}

func (j *replicaJournal) finishReconciliation() error {
	when := []byte(strconv.FormatInt(j.now().Unix(), 10))
	if err := j.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(replicaMetaBucket).Put(replicaReconciledKey, when)
	}); err != nil {
		return err
	}
	j.healthMu.Lock()
	j.reconciled = true
	j.lastErr = nil
	j.healthMu.Unlock()
	return nil
}

func replicaJournalAction(event nostr.Event) (string, string, bool) {
	switch event.Kind {
	case 31923:
		marker, ok := exactCalendarTag(event, "bitcoinwalk")
		if !ok || (marker != "occurrence-v1" && marker != "initial-proposal-v1") {
			return "", "", false
		}
		cityID, err := uniqueTag(event, "i")
		return "occurrence", cityID, err == nil
	case 5:
		cityID, err := uniqueTag(event, "i")
		return "cancellation", cityID, err == nil
	case 30302:
		var grant cityGrant
		if json.Unmarshal([]byte(event.Content), &grant) != nil {
			return "", "", false
		}
		return "grant", grant.CityID, true
	case 30304:
		var decision cityDecision
		if json.Unmarshal([]byte(event.Content), &decision) != nil {
			return "", "", false
		}
		return "decision", decision.CityID, true
	case eventModerationKind:
		moderation, err := parseEventModeration(event)
		// City/author/organizer suspension governs writes at the authoritative
		// source. A dedicated city receiver is read-only, so only event visibility
		// decisions belong in its ordered public-state stream.
		if err != nil || moderation.Scope != "event" {
			return "", "", false
		}
		return "moderation", moderation.CityID, true
	default:
		return "", "", false
	}
}

// recordAcceptedLocked is called only after Khatru confirms durable event
// storage, while the organizer policy mutex is held. It excludes drafts, chat
// and all unconfigured cities. Initial proposals for configured cities are
// retained as blocked targets so an exact approval can release one atomically.
func (j *replicaJournal) recordAcceptedLocked(policy *organizerPolicy, event nostr.Event) (*replicaJournalEntry, bool, error) {
	return j.recordEventLocked(policy, event, false)
}

func (j *replicaJournal) recordEventLocked(policy *organizerPolicy, event nostr.Event, recovered bool) (*replicaJournalEntry, bool, error) {
	action, cityID, relevant := replicaJournalAction(event)
	if !relevant {
		return nil, false, nil
	}
	destination, configured := j.registry.destination(cityID)
	if !configured {
		return nil, false, nil
	}
	stored := policy.byID(event.ID.Hex())
	if stored == nil || stored.ID != event.ID {
		return nil, false, errors.New("accepted source event is not durably stored")
	}
	entry := replicaJournalEntry{
		Action:      action,
		CityID:      cityID,
		Destination: destination,
		EventID:     event.ID.Hex(),
		Event:       event,
		AcceptedAt:  j.now().Unix(),
		Recovered:   recovered,
	}
	// Replicate the signed base occurrence even when a later moderation decision
	// currently hides it. Ordered moderation is delivered separately after the
	// target exists at the receiver. Tombstones, revocations and stale authority
	// still fail this retained-source check.
	if action == "occurrence" && policy.checkReplicaCalendarTarget(event) == nil {
		entry.EligibleAtAcceptance = true
	}
	if approval := policy.currentApproval(cityID); approval != nil {
		entry.CurrentApprovalID = approval.ID.Hex()
	}
	_, grantEvent, err := policy.grant(cityID)
	if err != nil {
		return nil, false, err
	}
	if grantEvent != nil {
		entry.CurrentGrantID = grantEvent.ID.Hex()
	}
	var bundle []byte
	bundleCode := ""
	if entry.EligibleAtAcceptance {
		bundle, err = policy.exportReplicaBundleLocked(cityID, event.ID.Hex())
		if err != nil {
			bundleCode = "bundle-unavailable"
			bundle = nil
		}
	}
	appended := false
	err = j.db.Update(func(tx *bbolt.Tx) error {
		entries := tx.Bucket(replicaEntriesBucket)
		events := tx.Bucket(replicaEventsBucket)
		heads := tx.Bucket(replicaHeadsBucket)
		queueEntry := entry
		if key := events.Get([]byte(entry.EventID)); key != nil {
			data := entries.Get(key)
			if data == nil || json.Unmarshal(data, &queueEntry) != nil {
				return errors.New("replica journal index is inconsistent")
			}
			queueEntry.EligibleAtAcceptance = entry.EligibleAtAcceptance
			queueEntry.CurrentApprovalID = entry.CurrentApprovalID
			queueEntry.CurrentGrantID = entry.CurrentGrantID
			data, err := json.Marshal(queueEntry)
			if err != nil {
				return err
			}
			if len(data) > 128*1024 {
				return errors.New("replica journal entry too large")
			}
			if err := entries.Put(key, data); err != nil {
				return err
			}
		} else {
			sequence, err := entries.NextSequence()
			if err != nil {
				return err
			}
			entry.Sequence = sequence
			queueEntry = entry
			key := sequenceKey(sequence)
			data, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			if len(data) > 128*1024 {
				return errors.New("replica journal entry too large")
			}
			if err := entries.Put(key, data); err != nil {
				return err
			}
			if err := events.Put([]byte(entry.EventID), key); err != nil {
				return err
			}
			if err := heads.Put([]byte(entry.CityID), key); err != nil {
				return err
			}
			appended = true
		}
		if action == "occurrence" && queueEntry.Destination == destination {
			var envelope *replicaDeliveryEnvelope
			blockedCode := bundleCode
			if queueEntry.EligibleAtAcceptance && bundleCode == "" {
				checkpoint := replicaCheckpoint{CityID: cityID, Destination: destination, OccurrenceID: event.ID.Hex(), SourceSequence: queueEntry.Sequence, CurrentApprovalID: entry.CurrentApprovalID, CurrentGrantID: entry.CurrentGrantID}
				prepared := replicaEnvelope(queueEntry, checkpoint, bundle)
				envelope = &prepared
			} else if blockedCode == "" {
				blockedCode = "source-ineligible"
			}
			if err := ensureReplicaOutboxTx(tx, queueEntry, envelope, blockedCode); err != nil {
				return err
			}
		}
		if action == "cancellation" {
			target, err := uniqueTag(event, "e")
			if err != nil {
				return err
			}
			control := replicaControlEnvelope(queueEntry, "cancellation", target)
			if err := suppressReplicaOutboxEventTx(tx, target, control); err != nil {
				return err
			}
		}
		if action == "decision" {
			var decision cityDecision
			if json.Unmarshal([]byte(event.Content), &decision) != nil {
				return errors.New("invalid replica decision")
			}
			if decision.Status == "revoked" && policy.currentApproval(cityID) == nil {
				control := replicaControlEnvelope(queueEntry, "revocation", "")
				needsStandalone, err := suppressReplicaOutboxCityTx(tx, cityID, control)
				if err != nil {
					return err
				}
				if needsStandalone {
					if err := queueReplicaRevocationTx(tx, queueEntry); err != nil {
						return err
					}
				}
			}
		}
		if action == "moderation" {
			if err := queueReplicaModerationTx(tx, queueEntry); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if action == "decision" {
		var decision cityDecision
		if json.Unmarshal([]byte(event.Content), &decision) == nil && decision.Status == "approved" && decision.InitialEventID != "" {
			initial := policy.byID(decision.InitialEventID)
			if initial == nil || initial.Kind != 31923 {
				return nil, false, errors.New("approved initial walk is unavailable for replication")
			}
			if _, _, err := j.recordEventLocked(policy, *initial, recovered); err != nil {
				return nil, false, err
			}
		}
	}
	if !appended {
		existing, found, err := j.entry(event.ID.Hex())
		return existing, false, chooseReplicaEntryError(found, err)
	}
	return &entry, true, nil
}

func recoveryActionOrder(action string) int {
	switch action {
	case "grant":
		return 0
	case "decision":
		return 1
	case "occurrence":
		return 2
	case "cancellation":
		return 3
	case "moderation":
		return 4
	default:
		return 5
	}
}

// recoverReplicaJournal closes the event-store/journal crash gap before the
// relay starts accepting traffic. Missing records are reconstructed only from
// the authoritative local event store and fixed operator registry. Transport
// must not start until this method completes successfully.
func (p *organizerPolicy) recoverReplicaJournal(journal *replicaJournal) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if journal == nil {
		return 0, errors.New("replica journal unavailable")
	}
	if err := journal.beginReconciliation(); err != nil {
		journal.setUnhealthy(err)
		return 0, err
	}
	const scanLimit = 100000
	candidates := make([]nostr.Event, 0)
	scanned := 0
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{5, 30302, 30304, eventModerationKind, 31923}}, scanLimit+1) {
		scanned++
		if scanned > scanLimit {
			err := errors.New("replica recovery scan limit exceeded")
			journal.setUnhealthy(err)
			return 0, err
		}
		_, cityID, relevant := replicaJournalAction(event)
		if !relevant {
			continue
		}
		if _, configured := journal.registry.destination(cityID); !configured {
			continue
		}
		if !event.CheckID() || !event.VerifySignature() {
			err := errors.New("invalid signed replication record in authoritative source")
			journal.setUnhealthy(err)
			return 0, err
		}
		candidates = append(candidates, event)
	}
	slices.SortFunc(candidates, func(a, b nostr.Event) int {
		if a.CreatedAt < b.CreatedAt {
			return -1
		}
		if a.CreatedAt > b.CreatedAt {
			return 1
		}
		actionA, _, _ := replicaJournalAction(a)
		actionB, _, _ := replicaJournalAction(b)
		if orderA, orderB := recoveryActionOrder(actionA), recoveryActionOrder(actionB); orderA != orderB {
			return orderA - orderB
		}
		return strings.Compare(a.ID.Hex(), b.ID.Hex())
	})
	recovered := 0
	for _, event := range candidates {
		_, appended, err := journal.recordEventLocked(p, event, true)
		if err != nil {
			journal.setUnhealthy(err)
			return recovered, err
		}
		if appended {
			recovered++
		}
	}
	if err := journal.finishReconciliation(); err != nil {
		journal.setUnhealthy(err)
		return recovered, err
	}
	return recovered, nil
}

func chooseReplicaEntryError(found bool, err error) error {
	if err != nil {
		return err
	}
	if !found {
		return errors.New("replica journal index is inconsistent")
	}
	return nil
}

func sequenceKey(sequence uint64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, sequence)
	return key
}

func (j *replicaJournal) entry(eventID string) (*replicaJournalEntry, bool, error) {
	var entry replicaJournalEntry
	found := false
	err := j.db.View(func(tx *bbolt.Tx) error {
		key := tx.Bucket(replicaEventsBucket).Get([]byte(eventID))
		if key == nil {
			return nil
		}
		data := tx.Bucket(replicaEntriesBucket).Get(key)
		if data == nil {
			return errors.New("replica journal entry missing")
		}
		if err := json.Unmarshal(data, &entry); err != nil {
			return err
		}
		found = true
		return nil
	})
	return &entry, found, err
}

func (j *replicaJournal) cityHead(cityID string) (uint64, error) {
	var sequence uint64
	err := j.db.View(func(tx *bbolt.Tx) error {
		key := tx.Bucket(replicaHeadsBucket).Get([]byte(cityID))
		if key == nil {
			return nil
		}
		if len(key) != 8 {
			return errors.New("invalid replica journal head")
		}
		sequence = binary.BigEndian.Uint64(key)
		return nil
	})
	return sequence, err
}

// freshReplicaCheckpoint refuses events that were never accepted by the
// authoritative relay, were accepted before becoming public, were canceled,
// belong to a revoked city, or were queued for a replaced destination.
func (p *organizerPolicy) freshReplicaCheckpoint(journal *replicaJournal, cityID, occurrenceID string) (*replicaCheckpoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.freshReplicaCheckpointLocked(journal, cityID, occurrenceID)
}

func (p *organizerPolicy) freshReplicaCheckpointLocked(journal *replicaJournal, cityID, occurrenceID string) (*replicaCheckpoint, error) {
	if journal == nil || !uuidPattern.MatchString(cityID) {
		return nil, errors.New("invalid replica checkpoint request")
	}
	if !journal.isReconciled() {
		return nil, errors.New("restricted: replica journal reconciliation incomplete")
	}
	destination, configured := journal.registry.destination(cityID)
	if !configured {
		return nil, errors.New("restricted: city is not configured for replication")
	}
	entry, found, err := journal.entry(occurrenceID)
	if err != nil {
		return nil, err
	}
	if !found || entry.Action != "occurrence" || entry.CityID != cityID || entry.Destination != destination || !entry.EligibleAtAcceptance {
		return nil, errors.New("restricted: occurrence lacks an eligible source acceptance")
	}
	event := p.byID(occurrenceID)
	if event == nil || event.Kind != 31923 || p.checkCalendarRead(*event) != nil {
		return nil, errors.New("restricted: occurrence is no longer public at the source")
	}
	approval := p.currentApproval(cityID)
	if approval == nil {
		return nil, errors.New("restricted: city is no longer approved")
	}
	_, grantEvent, err := p.grant(cityID)
	if err != nil || grantEvent == nil {
		return nil, errors.New("restricted: city grant unavailable")
	}
	head, err := journal.cityHead(cityID)
	if err != nil || head < entry.Sequence {
		return nil, errors.New("replica journal head unavailable")
	}
	return &replicaCheckpoint{
		CityID:       cityID,
		Destination:  destination,
		OccurrenceID: occurrenceID,
		// The city head proves reconciliation reached at least this event, but
		// each delivery retains its own durable acceptance sequence. Reusing the
		// latest head for a backfill batch would give distinct occurrences the
		// same receiver checkpoint and correctly trigger a conflict.
		SourceSequence:    entry.Sequence,
		CurrentApprovalID: approval.ID.Hex(),
		CurrentGrantID:    grantEvent.ID.Hex(),
	}, nil
}

func attachReplicaJournal(policy *organizerPolicy, journal *replicaJournal) {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	policy.replicaJournal = journal
}
