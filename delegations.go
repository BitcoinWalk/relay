package main

import (
	"encoding/json"
	"errors"
	"fiatjaf.com/nostr"
	"strconv"
)

const delegationKind nostr.Kind = 30305
const delegationAcceptanceKind nostr.Kind = 30306

// Version 2 designates a host for ONE signed occurrence. It grants no city permissions.
type cityDelegation struct {
	Version    int             `json:"version"`
	CityID     string          `json:"cityId"`
	EventID    string          `json:"eventId"`
	Nominee    string          `json:"nomineePubkey"`
	Action     string          `json:"action"`
	PreviousID string          `json:"previousId"`
	ExpiresAt  nostr.Timestamp `json:"expiresAt"`
}
type delegationAcceptance struct {
	Version      int    `json:"version"`
	CityID       string `json:"cityId"`
	EventID      string `json:"eventId"`
	InvitationID string `json:"invitationId"`
}

func (p *organizerPolicy) latestWalkDelegation(walk nostr.Event) *nostr.Event {
	var latest *nostr.Event
	for _, author := range []nostr.PubKey{p.admin, walk.PubKey} {
		for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{delegationKind}, Authors: []nostr.PubKey{author}, Tags: nostr.TagMap{"e": []string{walk.ID.Hex()}}}, 1) {
			if latest == nil || event.CreatedAt > latest.CreatedAt || event.CreatedAt == latest.CreatedAt && event.ID.Hex() < latest.ID.Hex() {
				copy := event
				latest = &copy
			}
		}
	}
	return latest
}
func exactDelegationTags(event nostr.Event, expected map[string]string) bool {
	if len(event.Tags) != len(expected) {
		return false
	}
	for name, value := range expected {
		actual, ok := exactCalendarTag(event, name)
		if !ok || actual != value {
			return false
		}
	}
	return true
}
func (p *organizerPolicy) delegationWalk(id, cityID string) (*nostr.Event, error) {
	walk := p.byID(id)
	if walk == nil || walk.Kind != 31923 {
		return nil, errors.New("invalid: published walk not found")
	}
	city, ok := exactCalendarTag(*walk, "i")
	if !ok || city != cityID {
		return nil, errors.New("invalid: walk belongs to another city")
	}
	if err := p.checkCalendarRead(*walk); err != nil {
		return nil, err
	}
	return walk, nil
}
func walkDelegationEnd(walk nostr.Event) nostr.Timestamp {
	value, ok := exactCalendarTag(walk, "end")
	if !ok {
		value, _ = exactCalendarTag(walk, "start")
	}
	result, _ := strconv.ParseInt(value, 10, 64)
	return nostr.Timestamp(result)
}
func (p *organizerPolicy) checkDelegation(event nostr.Event) error {
	var control cityDelegation
	if len(event.Content) > 2000 || json.Unmarshal([]byte(event.Content), &control) != nil || control.Version != 2 || !uuidPattern.MatchString(control.CityID) {
		return errors.New("invalid: invitation must delegate a single walk (version 2)")
	}
	walk, err := p.delegationWalk(control.EventID, control.CityID)
	if err != nil {
		return err
	}
	if event.PubKey != p.admin && event.PubKey != walk.PubKey {
		return errors.New("restricted: only the walk author or super-admin may delegate this walk")
	}
	nominee, err := nostr.PubKeyFromHex(control.Nominee)
	if err != nil || nominee == walk.PubKey {
		return errors.New("invalid: choose another host")
	}
	d, err := uniqueTag(event, "d")
	if err != nil || d == control.CityID || !workflowAddress(d, control.CityID) || !exactDelegationTags(event, map[string]string{"d": d, "i": control.CityID, "e": control.EventID, "p": control.Nominee, "action": control.Action}) {
		return errors.New("invalid: walk invitation scope")
	}
	if old := p.find(delegationKind, &event.PubKey, d); old != nil && old.ID != event.ID {
		return errors.New("restricted: invitation addresses are immutable")
	}
	latest := p.latestWalkDelegation(*walk)
	if latest != nil && latest.ID == event.ID {
		return nil
	}
	if latest == nil && control.PreviousID != "" || latest != nil && (control.PreviousID != latest.ID.Hex() || event.CreatedAt <= latest.CreatedAt) {
		return errors.New("restricted: delegation changed; reload before signing")
	}
	switch control.Action {
	case "invite":
		if control.ExpiresAt <= event.CreatedAt || control.ExpiresAt > event.CreatedAt+7*86400 || control.ExpiresAt <= nostr.Now() || control.ExpiresAt > walkDelegationEnd(*walk) {
			return errors.New("invalid: invitation must expire within seven days and before the walk ends")
		}
	case "revoke":
		if latest == nil || control.ExpiresAt != 0 || control.Nominee != func() string { var c cityDelegation; json.Unmarshal([]byte(latest.Content), &c); return c.Nominee }() {
			return errors.New("invalid: no matching delegation to revoke")
		}
	default:
		return errors.New("invalid: invitation action")
	}
	return nil
}
func (p *organizerPolicy) checkDelegationAcceptance(event nostr.Event) error {
	var acceptance delegationAcceptance
	if len(event.Content) > 1000 || json.Unmarshal([]byte(event.Content), &acceptance) != nil || acceptance.Version != 2 {
		return errors.New("invalid: walk invitation acceptance")
	}
	invite := p.byID(acceptance.InvitationID)
	if invite == nil || invite.Kind != delegationKind {
		return errors.New("invalid: invitation missing")
	}
	var control cityDelegation
	if json.Unmarshal([]byte(invite.Content), &control) != nil || control.Version != 2 || control.Action != "invite" || control.CityID != acceptance.CityID || control.EventID != acceptance.EventID || control.Nominee != event.PubKey.Hex() {
		return errors.New("restricted: invitation belongs to another identity or walk")
	}
	if !exactDelegationTags(event, map[string]string{"d": invite.ID.Hex(), "i": control.CityID, "e": invite.ID.Hex(), "walk": control.EventID}) {
		return errors.New("invalid: acceptance scope")
	}
	walk, err := p.delegationWalk(control.EventID, control.CityID)
	if err != nil {
		return err
	}
	latest := p.latestWalkDelegation(*walk)
	if latest == nil || latest.ID != invite.ID {
		return errors.New("restricted: invitation revoked or superseded")
	}
	if old := p.find(delegationAcceptanceKind, &event.PubKey, invite.ID.Hex()); old != nil {
		if old.ID == event.ID {
			return nil
		}
		return errors.New("restricted: invitation already accepted")
	}
	if event.CreatedAt < invite.CreatedAt || event.CreatedAt > control.ExpiresAt || nostr.Now() > control.ExpiresAt {
		return errors.New("restricted: invitation expired")
	}
	return nil
}
