package main

import (
	"context"
	"encoding/json"
	"errors"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
)

// replicaScope is operator-owned configuration, never supplied by an EVENT.
// This gate is intentionally not installed in the ordinary EVENT path. The
// inactive receiver calls it only after validating and staging a complete
// dependency envelope. Real transport and ordered removals remain disabled.
type replicaScope struct {
	CityID      string
	ServiceKey  nostr.PubKey
	Destination string
}

// checkReplicaOccurrence validates against already verified local policy state.
// The service authenticates as itself, never as the original event author.
// Call under the policy mutex together with storage when eventually integrated.
func (p *organizerPolicy) checkReplicaOccurrence(ctx context.Context, event nostr.Event, scope replicaScope) error {
	if !uuidPattern.MatchString(scope.CityID) || scope.ServiceKey == (nostr.PubKey{}) || scope.ServiceKey == p.admin {
		return errors.New("restricted: invalid replication scope")
	}
	if !khatru.IsAuthed(ctx, scope.ServiceKey) {
		return errors.New("auth-required: authenticate as configured replication service")
	}
	if event.Kind != 31923 {
		return errors.New("restricted: replication permits calendar occurrences only")
	}
	if !event.CheckID() || !event.VerifySignature() {
		return errors.New("invalid: replication event signature or ID")
	}
	city, err := uniqueTag(event, "i")
	if err != nil || city != scope.CityID {
		return errors.New("restricted: replication city mismatch")
	}
	return p.checkReplicaCalendarTarget(event)
}

func (p *organizerPolicy) checkReplicaCalendarTarget(event nostr.Event) error {
	marker, ok := exactCalendarTag(event, "bitcoinwalk")
	if !ok {
		return errors.New("restricted: replication requires a released calendar occurrence")
	}
	if marker == "occurrence-v1" {
		return p.checkReplicaCalendar(event)
	}
	if marker != "initial-proposal-v1" {
		return errors.New("restricted: replication requires a released calendar occurrence")
	}
	if err := p.checkCalendarRead(event); err != nil {
		return err
	}
	cityID, err := uniqueTag(event, "i")
	if err != nil {
		return err
	}
	approval := p.currentApproval(cityID)
	if approval == nil {
		return errors.New("restricted: initial walk has no current city approval")
	}
	var decision cityDecision
	if json.Unmarshal([]byte(approval.Content), &decision) != nil || decision.InitialEventID != event.ID.Hex() {
		return errors.New("restricted: initial walk was not released by the current approval")
	}
	grant, _, err := p.grant(cityID)
	if err != nil {
		return err
	}
	if !p.canEdit(event.PubKey, grant) {
		return errors.New("restricted: occurrence author is not a current city editor")
	}
	return nil
}

// checkReplicaCalendar validates retained source history without reapplying the
// wall-clock publication horizon. That horizon protects new writes; applying it
// again during restart recovery would make an already accepted walk impossible
// to backfill as soon as its start time passed. Current approval, current editor
// authority, schema and retained tombstones still fail closed here.
func (p *organizerPolicy) checkReplicaCalendar(event nostr.Event) error {
	if err := p.checkOrganizerCalendar(event, false); err != nil {
		return err
	}
	cityID, err := uniqueTag(event, "i")
	if err != nil {
		return err
	}
	approvalID, err := calendarReference(event, "city-approval")
	if err != nil {
		return err
	}
	approval := p.currentApproval(cityID)
	if approval == nil || approval.ID.Hex() != approvalID {
		return errors.New("restricted: occurrence must reference the current city approval")
	}
	grant, _, err := p.grant(cityID)
	if err != nil {
		return err
	}
	if !p.canEdit(event.PubKey, grant) {
		return errors.New("restricted: occurrence author is not a current city editor")
	}
	return nil
}
