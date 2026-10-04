package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"

	"fiatjaf.com/nostr"
)

const sponsorshipKind nostr.Kind = 30311
const sponsorshipID = "bitcoinwalk-sponsorship"

type sponsorshipScope struct {
	Type    string `json:"type"`
	CityID  string `json:"cityId"`
	Address string `json:"address,omitempty"`
}
type sponsorship struct {
	Version            int              `json:"version"`
	Scope              sponsorshipScope `json:"scope"`
	Mode               string           `json:"mode"`
	SponsorPubkey      string           `json:"sponsorPubkey,omitempty"`
	Website            string           `json:"website,omitempty"`
	StartsAt           *int64           `json:"startsAt,omitempty"`
	EndsAt             *int64           `json:"endsAt,omitempty"`
	PreviousRevisionID string           `json:"previousRevisionId,omitempty"`
}

func sponsorshipKey(value sponsorshipScope) string {
	if value.Type == "city" {
		return "city:" + value.CityID
	}
	return "walk:" + value.CityID + ":" + value.Address
}
func publicHTTPS(value string) bool {
	if value == "" {
		return true
	}
	u, err := url.Parse(value)
	host := u.Hostname()
	return err == nil && u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") && strings.Contains(host, ".") && net.ParseIP(host) == nil && !strings.HasSuffix(host, ".localhost") && host != "localhost" && len(value) <= 2048
}
func parseSponsorship(event nostr.Event) (sponsorship, error) {
	var value sponsorship
	decoder := json.NewDecoder(bytes.NewBufferString(event.Content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return value, errors.New("invalid: sponsorship JSON required")
	}
	if value.Version != 1 || !uuidPattern.MatchString(value.Scope.CityID) || (value.Scope.Type != "city" && value.Scope.Type != "walk") || value.Scope.Type == "city" && value.Scope.Address != "" || value.Scope.Type == "walk" && !sizeOK(value.Scope.Address, 1, 512) {
		return value, errors.New("invalid: sponsorship scope")
	}
	if value.Mode != "inherit" && value.Mode != "hidden" && value.Mode != "empty" && value.Mode != "sponsor" || value.Scope.Type == "city" && value.Mode == "inherit" {
		return value, errors.New("invalid: sponsorship mode")
	}
	if value.Mode == "sponsor" && !hexKeyPattern.MatchString(value.SponsorPubkey) || value.Mode != "sponsor" && (value.SponsorPubkey != "" || value.Website != "") || !publicHTTPS(value.Website) {
		return value, errors.New("invalid: sponsorship details")
	}
	if value.StartsAt != nil && *value.StartsAt < 0 || value.EndsAt != nil && *value.EndsAt <= 0 || value.StartsAt != nil && value.EndsAt != nil && *value.EndsAt <= *value.StartsAt {
		return value, errors.New("invalid: sponsorship dates")
	}
	if value.PreviousRevisionID != "" && !hexKeyPattern.MatchString(value.PreviousRevisionID) {
		return value, errors.New("invalid: sponsorship previous revision")
	}
	key := sponsorshipKey(value.Scope)
	d, de := uniqueTag(event, "d")
	indexed, ie := uniqueTag(event, "i")
	scope, se := uniqueTag(event, "scope")
	mode, me := uniqueTag(event, "mode")
	client, ce := uniqueTag(event, "client")
	prefix := sponsorshipID + ":" + key + ":"
	if de != nil || ie != nil || se != nil || me != nil || ce != nil || !strings.HasPrefix(d, prefix) || !uuidPattern.MatchString(strings.TrimPrefix(d, prefix)) || indexed != sponsorshipID || scope != key || mode != value.Mode || client != "bitcoinwalk.org" {
		return value, errors.New("invalid: sponsorship tags")
	}
	previousCount, previousID := 0, ""
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			return value, errors.New("invalid: sponsorship tag")
		}
		if tag[0] == "e" {
			if len(tag) != 4 || tag[2] != "" || tag[3] != "previous" {
				return value, errors.New("invalid: sponsorship previous tag")
			}
			previousCount++
			previousID = tag[1]
		} else if (tag[0] != "d" && tag[0] != "i" && tag[0] != "scope" && tag[0] != "mode" && tag[0] != "client") || len(tag) != 2 {
			return value, errors.New("restricted: unsupported sponsorship tag")
		}
	}
	if (value.PreviousRevisionID == "") != (previousCount == 0) || previousCount > 1 || previousID != value.PreviousRevisionID {
		return value, errors.New("invalid: sponsorship previous reference mismatch")
	}
	return value, nil
}
func (p *organizerPolicy) latestSponsorship(key string) *nostr.Event {
	var latest *nostr.Event
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{sponsorshipKind}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"scope": []string{key}}}, 10001) {
		copy := event
		if latest == nil || copy.CreatedAt > latest.CreatedAt || copy.CreatedAt == latest.CreatedAt && copy.ID.Hex() > latest.ID.Hex() {
			latest = &copy
		}
	}
	return latest
}
func (p *organizerPolicy) checkSponsorship(event nostr.Event) error {
	if event.PubKey != p.admin {
		return errors.New("restricted: only the super-admin can change sponsorships")
	}
	value, err := parseSponsorship(event)
	if err != nil {
		return err
	}
	if p.byID(event.ID.Hex()) != nil {
		return nil
	}
	current := p.latestSponsorship(sponsorshipKey(value.Scope))
	if current == nil && value.PreviousRevisionID != "" {
		return errors.New("invalid: first sponsorship revision cannot reference a predecessor")
	}
	if current != nil && (value.PreviousRevisionID != current.ID.Hex() || event.CreatedAt <= current.CreatedAt) {
		return errors.New("restricted: sponsorship changed; reload before publishing")
	}
	return nil
}
