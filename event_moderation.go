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

// Retained super-admin decisions: visibility and city suspension are
// city-scoped; organizer suspension is global. None alters ownership or
// cancellation.
type eventModeration struct {
	CityID   string `json:"cityId,omitempty"`
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
	if !sizeOK(strings.TrimSpace(m.Reason), 1, 500) || len(m.Target) > 300 {
		return m, errors.New("invalid: moderation fields")
	}
	switch m.Scope {
	case "event":
		parts := strings.SplitN(m.Target, ":", 3)
		if !uuidPattern.MatchString(m.CityID) || len(parts) != 3 || parts[0] != "31923" || !hexKeyPattern.MatchString(parts[1]) || parts[2] == "" || !hexKeyPattern.MatchString(m.EventID) || (m.Status != "hidden" && m.Status != "visible") {
			return m, errors.New("invalid: event moderation target")
		}
	case "city", "author":
		if !uuidPattern.MatchString(m.CityID) || m.EventID != "" || (m.Status != "suspended" && m.Status != "active") || (m.Scope == "city" && m.Target != m.CityID) || (m.Scope == "author" && !hexKeyPattern.MatchString(m.Target)) {
			return m, errors.New("invalid: suspension target")
		}
	case "organizer":
		if m.CityID != "" || m.EventID != "" || !hexKeyPattern.MatchString(m.Target) || (m.Status != "suspended" && m.Status != "active") {
			return m, errors.New("invalid: organizer suspension target")
		}
	default:
		return m, errors.New("invalid: moderation scope")
	}
	if m.Previous != "" && !hexKeyPattern.MatchString(m.Previous) {
		return m, errors.New("invalid: moderation previous decision")
	}
	expected := map[string]string{"m": moderationKey(m), "status": m.Status, "client": "bitcoinwalk.org"}
	d, err := uniqueTag(event, "d")
	if m.Scope == "organizer" {
		if err != nil || !strings.HasPrefix(d, "organizer:") || !uuidPattern.MatchString(strings.TrimPrefix(d, "organizer:")) {
			return m, errors.New("invalid: retained organizer moderation address")
		}
	} else {
		expected["i"] = m.CityID
		if err != nil || d == m.CityID || !workflowAddress(d, m.CityID) {
			return m, errors.New("invalid: retained moderation address")
		}
	}
	if len(event.Tags) != len(expected)+1 {
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
func (p *organizerPolicy) latestOrganizerModeration(key string) *nostr.Event {
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{eventModerationKind}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"m": []string{"organizer:" + key}}}, 1) {
		return &event
	}
	// Legacy city-scoped author decisions become the migration fallback. Once a
	// global organizer decision exists, later legacy decisions cannot override it.
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{eventModerationKind}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"m": []string{"author:" + key}}}, 1) {
		return &event
	}
	return nil
}
func (p *organizerPolicy) organizerModerationStatus(key string) string {
	event := p.latestOrganizerModeration(key)
	if event == nil {
		return ""
	}
	m, err := parseEventModeration(*event)
	if err != nil || m.Target != key || (m.Scope != "organizer" && m.Scope != "author") {
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
	if stored := p.byID(event.ID.Hex()); stored != nil {
		return nil
	} // exact retry cannot replace a newer head
	if m.Scope == "organizer" {
		previous := p.latestOrganizerModeration(m.Target)
		if previous == nil {
			if m.Previous != "" {
				return errors.New("restricted: stale organizer moderation decision")
			}
		} else if m.Previous != previous.ID.Hex() || event.CreatedAt <= previous.CreatedAt {
			return errors.New("restricted: organizer moderation changed; reload before signing")
		}
		return nil
	}
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
	if s := p.moderationStatus(cityID, "city:"+cityID); s != "" && s != "active" {
		return errors.New("restricted: city publishing suspended by BitcoinWalk super-admin")
	}
	if s := p.organizerModerationStatus(event.PubKey.Hex()); s != "" && s != "active" {
		return errors.New("restricted: organizer publishing suspended by BitcoinWalk super-admin")
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
