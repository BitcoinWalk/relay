package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
)

// replicaBundle is a bounded dependency snapshot, not a signed freshness claim.
// Policy records are internal replication data, never a public calendar feed.
// In particular creator provenance must not be exposed as a newly public proposal.
type replicaBundle struct {
	Version      int           `json:"version"`
	CityID       string        `json:"cityId"`
	Events       []nostr.Event `json:"events"`
	OccurrenceID string        `json:"occurrenceId"`
}

// validateReplicaBundle validates in a disposable, unserved database. It does
// NOT install records in a live relay and cannot establish current source state.
// A receiver must also check authenticated source ordering and existing
// destination tombstones/revocations before making the occurrence visible.
func validateReplicaBundle(ctx context.Context, data []byte, scope replicaScope, admin nostr.PubKey) error {
	if len(data) > 256*1024 {
		return errors.New("restricted: replica bundle too large")
	}
	if !uuidPattern.MatchString(scope.CityID) || scope.ServiceKey == (nostr.PubKey{}) || scope.ServiceKey == admin || !khatru.IsAuthed(ctx, scope.ServiceKey) {
		return errors.New("restricted: unauthenticated or invalid replica scope")
	}
	var bundle replicaBundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid: trailing bundle data")
	}
	if bundle.Version != 1 || bundle.CityID != scope.CityID || len(bundle.Events) < 4 || len(bundle.Events) > 6 {
		return errors.New("invalid: bundle envelope")
	}
	seen := map[string]bool{}
	for _, event := range bundle.Events {
		if seen[event.ID.Hex()] || !event.CheckID() || !event.VerifySignature() {
			return errors.New("invalid: duplicate or corrupt bundle event")
		}
		seen[event.ID.Hex()] = true
		switch event.Kind {
		case 30303:
			city, err := parseDraft(event)
			if err != nil || city.CityID != scope.CityID {
				return errors.New("invalid: bundle city revision")
			}
		case 30302:
			var grant cityGrant
			if event.PubKey != admin || json.Unmarshal([]byte(event.Content), &grant) != nil || grant.CityID != scope.CityID {
				return errors.New("invalid: bundle grant")
			}
		case 30304:
			var decision cityDecision
			if event.PubKey != admin || json.Unmarshal([]byte(event.Content), &decision) != nil || decision.CityID != scope.CityID || decision.Status != "approved" {
				return errors.New("invalid: bundle approval")
			}
		case 31923:
			city, err := uniqueTag(event, "i")
			if err != nil || city != scope.CityID {
				return errors.New("invalid: bundle occurrence city")
			}
		default:
			return errors.New("restricted: unsupported bundle kind")
		}
	}
	dir, err := os.MkdirTemp("", "bitcoinwalk-replica-validation-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db := &boltdb.BoltBackend{Path: filepath.Join(dir, "snapshot.db")}
	if err := db.Init(); err != nil {
		return err
	}
	defer db.Close()
	policy := &organizerPolicy{db: db, admin: admin}
	// Revisions are schema/signature-checked provenance, not new proposals. Their
	// authorization is established by the admin-signed grant and approval below.
	for _, event := range bundle.Events {
		if event.Kind == 30303 {
			if err := db.SaveEvent(event); err != nil {
				return err
			}
		}
	}
	for _, kind := range []nostr.Kind{30302, 31923, 30304} {
		for _, event := range bundle.Events {
			if event.Kind != kind {
				continue
			}
			if kind == 31923 {
				marker, ok := exactCalendarTag(event, "bitcoinwalk")
				if event.ID.Hex() == bundle.OccurrenceID && ok && marker == "occurrence-v1" {
					continue
				}
				if !ok || marker != "initial-proposal-v1" {
					return errors.New("restricted: unrelated calendar dependency")
				}
			}
			// This synthetic context invokes existing semantic validators ONLY in this
			// disposable DB. It never grants authentication on a socket or live relay.
			if err := policy.check(khatru.ForceSetAuthed(ctx, event.PubKey), event); err != nil {
				return err
			}
			if err := db.SaveEvent(event); err != nil {
				return err
			}
		}
	}
	var target *nostr.Event
	for _, event := range bundle.Events {
		if event.ID.Hex() == bundle.OccurrenceID {
			copy := event
			target = &copy
		}
	}
	if target == nil {
		return errors.New("invalid: missing occurrence")
	}
	if err := policy.checkReplicaOccurrence(ctx, *target, scope); err != nil {
		return err
	}
	grant, _, err := policy.grant(scope.CityID)
	if err != nil || grant == nil {
		return errors.New("invalid: missing grant")
	}
	approval := policy.currentApproval(scope.CityID)
	if approval == nil {
		return errors.New("invalid: missing approval")
	}
	var decision cityDecision
	if json.Unmarshal([]byte(approval.Content), &decision) != nil {
		return errors.New("invalid: decision")
	}
	required := map[string]bool{grant.CreatorRevisionID: true, decision.RevisionID: true, approval.ID.Hex(): true, bundle.OccurrenceID: true}
	_, grantEvent, _ := policy.grant(scope.CityID)
	required[grantEvent.ID.Hex()] = true
	for _, id := range decisionInitialIDs(decision) {
		required[id] = true
	}
	for id := range seen {
		if !required[id] {
			return errors.New("restricted: unrelated bundle dependency")
		}
	}
	return nil
}

// exportReplicaBundle reads only an occurrence already stored and currently
// eligible at the authoritative source. No arbitrary caller-supplied event or
// URL is forwarded. Lock serializes the snapshot with normal policy writes.
func (p *organizerPolicy) exportReplicaBundle(cityID, occurrenceID string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exportReplicaBundleLocked(cityID, occurrenceID)
}

func (p *organizerPolicy) exportReplicaBundleLocked(cityID, occurrenceID string) ([]byte, error) {
	if !uuidPattern.MatchString(cityID) {
		return nil, errors.New("invalid: replica city")
	}
	for range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{eventModerationKind}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"i": []string{cityID}}}, 1) {
		return nil, errors.New("replication of moderated cities awaits receiver support")
	}
	occurrence := p.byID(occurrenceID)
	if occurrence == nil || occurrence.Kind != 31923 {
		return nil, errors.New("restricted: occurrence not accepted by source")
	}
	if city, err := uniqueTag(*occurrence, "i"); err != nil || city != cityID {
		return nil, errors.New("restricted: replica city mismatch")
	}
	if err := p.checkReplicaCalendarTarget(*occurrence); err != nil {
		return nil, err
	}
	grant, grantEvent, err := p.grant(cityID)
	if err != nil {
		return nil, err
	}
	if grant == nil || grantEvent == nil {
		return nil, errors.New("restricted: city grant unavailable")
	}
	approval := p.currentApproval(cityID)
	if approval == nil {
		return nil, errors.New("restricted: city approval unavailable")
	}
	var decision cityDecision
	if json.Unmarshal([]byte(approval.Content), &decision) != nil {
		return nil, errors.New("invalid: approval")
	}
	bundle := replicaBundle{Version: 1, CityID: cityID, OccurrenceID: occurrenceID}
	seen := map[string]bool{}
	add := func(event *nostr.Event) error {
		if event == nil || !event.CheckID() || !event.VerifySignature() {
			return errors.New("invalid: missing or corrupt signed dependency")
		}
		if !seen[event.ID.Hex()] {
			bundle.Events = append(bundle.Events, *event)
			seen[event.ID.Hex()] = true
		}
		return nil
	}
	for _, event := range []*nostr.Event{p.byID(grant.CreatorRevisionID), grantEvent, p.byID(decision.RevisionID)} {
		if err := add(event); err != nil {
			return nil, err
		}
	}
	// Include only an initial walk explicitly released by this exact approval.
	for _, initialID := range decisionInitialIDs(decision) {
		initial := p.byID(initialID)
		if initial == nil {
			return nil, errors.New("invalid: released initial walk missing")
		}
		if err := p.checkCalendarRead(*initial); err != nil {
			return nil, err
		}
		if err := add(initial); err != nil {
			return nil, err
		}
	}
	for _, event := range []*nostr.Event{approval, occurrence} {
		if err := add(event); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	if len(data) > 256*1024 {
		return nil, fmt.Errorf("restricted: replica bundle exceeds 256 KiB")
	}
	return data, nil
}
