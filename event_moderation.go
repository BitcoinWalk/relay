package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fiatjaf.com/nostr"
	"io"
	"regexp"
	"strings"
)

const eventModerationKind nostr.Kind = 30310

var hexKeyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Retained super-admin decisions: visibility is address-scoped, suspension is
// city-scoped (optionally one author). Neither alters ownership or cancellation.
type eventModeration struct {
	CityID   string `json:"cityId"`
	Scope    string `json:"scope"`
	Target   string `json:"target"`
	EventID  string `json:"eventId,omitempty"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Previous string `json:"previous,omitempty"`
}

func moderationKey(m eventModeration) string { return m.Scope + ":" + m.Target }
func calendarAddress(event nostr.Event) string {
	d, _ := uniqueTag(event, "d")
	return "31923:" + event.PubKey.Hex() + ":" + d
}
func parseEventModeration(event nostr.Event) (eventModeration, error) {
	var m eventModeration
	dec := json.NewDecoder(bytes.NewBufferString(event.Content))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || dec.Decode(new(any)) != io.EOF {
		return m, errors.New("invalid: moderation JSON")
	}
	if !uuidPattern.MatchString(m.CityID) || !sizeOK(strings.TrimSpace(m.Reason), 1, 500) || len(m.Target) > 300 {
		return m, errors.New("invalid: moderation fields")
	}
	switch m.Scope {
	case "event":
		parts := strings.SplitN(m.Target, ":", 3)
		if len(parts) != 3 || parts[0] != "31923" || !hexKeyPattern.MatchString(parts[1]) || parts[2] == "" || !hexKeyPattern.MatchString(m.EventID) || (m.Status != "hidden" && m.Status != "visible") {
			return m, errors.New("invalid: event moderation target")
		}
	case "city", "author":
		if m.EventID != "" || (m.Status != "suspended" && m.Status != "active") || (m.Scope == "city" && m.Target != m.CityID) || (m.Scope == "author" && !hexKeyPattern.MatchString(m.Target)) {
			return m, errors.New("invalid: suspension target")
		}
	default:
		return m, errors.New("invalid: moderation scope")
	}
	if m.Previous != "" && !hexKeyPattern.MatchString(m.Previous) {
		return m, errors.New("invalid: moderation previous decision")
	}
	expected := map[string]string{"i": m.CityID, "m": moderationKey(m), "status": m.Status, "client": "bitcoinwalk.org"}
	d, err := uniqueTag(event, "d")
	if err != nil || d == m.CityID || !workflowAddress(d, m.CityID) {
		return m, errors.New("invalid: retained moderation address")
	}
	if len(event.Tags) != 5 {
		return m, errors.New("invalid: moderation tags")
	}
	for name, value := range expected {
		v, e := uniqueTag(event, name)
		if e != nil || v != value {
			return m, errors.New("invalid: moderation tags")
		}
	}
	for _, tag := range event.Tags {
		if len(tag) != 2 {
			return m, errors.New("invalid: moderation tag shape")
		}
	}
	return m, nil
}
func (p *organizerPolicy) latestEventModeration(cityID, key string) *nostr.Event {
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{eventModerationKind}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"i": []string{cityID}, "m": []string{key}}}, 1) {
		return &event
	}
	return nil
}
func (p *organizerPolicy) moderationStatus(cityID, key string) string {
	event := p.latestEventModeration(cityID, key)
	if event == nil {
		return ""
	}
	m, err := parseEventModeration(*event)
	if err != nil {
		return "invalid"
	}
	return m.Status
}
func (p *organizerPolicy) checkEventModeration(event nostr.Event) error {
	if event.PubKey != p.admin {
		return errors.New("restricted: only super-admin may moderate events or suspend publishing")
	}
	m, err := parseEventModeration(event)
	if err != nil {
		return err
	}
	if p.replicaJournal != nil {
		if _, configured := p.replicaJournal.registry.destination(m.CityID); configured {
			return errors.New("restricted: moderation for replicated cities awaits receiver support; nothing changed")
		}
	}
	if stored := p.byID(event.ID.Hex()); stored != nil {
		return nil
	} // exact retry cannot replace a newer head
	grant, _, err := p.grant(m.CityID)
	if err != nil || grant == nil {
		return errors.New("restricted: moderation requires an existing city")
	}
	if m.Scope == "event" {
		target := p.byID(m.EventID)
		if target == nil || target.Kind != 31923 || mustCalendarCity(*target) != m.CityID || calendarAddress(*target) != m.Target {
			return errors.New("restricted: moderation event does not match city and address")
		}
	}
	previous := p.latestEventModeration(m.CityID, moderationKey(m))
	if previous == nil {
		if m.Previous != "" {
			return errors.New("restricted: stale moderation decision")
		}
	} else if m.Previous != previous.ID.Hex() || event.CreatedAt <= previous.CreatedAt {
		return errors.New("restricted: moderation changed; reload before signing")
	}
	return nil
}
func (p *organizerPolicy) checkPublishingSuspension(event nostr.Event) error {
	cityID := mustCalendarCity(event)
	for _, key := range []string{"city:" + cityID, "author:" + event.PubKey.Hex()} {
		s := p.moderationStatus(cityID, key)
		if s != "" && s != "active" {
			return errors.New("restricted: publishing suspended by BitcoinWalk super-admin")
		}
	}
	return p.checkEventVisibility(event)
}
func (p *organizerPolicy) checkEventVisibility(event nostr.Event) error {
	s := p.moderationStatus(mustCalendarCity(event), "event:"+calendarAddress(event))
	if s != "" && s != "visible" {
		return errors.New("restricted: walk address is hidden by BitcoinWalk super-admin")
	}
	return nil
}
