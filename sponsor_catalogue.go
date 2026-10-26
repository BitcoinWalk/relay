package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"fiatjaf.com/nostr"
)

const sponsorCatalogueKind nostr.Kind = 30313
const sponsorCatalogueID = "bitcoinwalk-sponsor-catalogue"
const sponsorCatalogueAssetCityID = "00000000-0000-4000-8000-000000000000"

type sponsorCatalogueRecord struct {
	Version            int    `json:"version"`
	RecordType         string `json:"recordType"`
	SponsorPubkey      string `json:"sponsorPubkey"`
	State              string `json:"state,omitempty"`
	Status             string `json:"status,omitempty"`
	DisplayName        string `json:"displayName"`
	Website            string `json:"website,omitempty"`
	LogoHash           string `json:"logoHash,omitempty"`
	Source             string `json:"source,omitempty"`
	SourceEventID      string `json:"sourceEventId,omitempty"`
	SourceCityID       string `json:"sourceCityId,omitempty"`
	SourceScopeKey     string `json:"sourceScopeKey,omitempty"`
	PreviousRevisionID string `json:"previousRevisionId,omitempty"`
}

func parseSponsorCatalogue(event nostr.Event) (sponsorCatalogueRecord, error) {
	var value sponsorCatalogueRecord
	decoder := json.NewDecoder(bytes.NewBufferString(event.Content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return value, errors.New("invalid: sponsor catalogue JSON required")
	}
	if value.Version != 1 || !hexKeyPattern.MatchString(value.SponsorPubkey) || strings.TrimSpace(value.DisplayName) != value.DisplayName || !sizeOK(value.DisplayName, 1, 80) || !publicHTTPS(value.Website) || value.PreviousRevisionID != "" && !hexKeyPattern.MatchString(value.PreviousRevisionID) {
		return value, errors.New("invalid: sponsor catalogue identity")
	}
	marker := value.State
	if value.RecordType == "claim" {
		if event.PubKey.Hex() != value.SponsorPubkey || value.State != "active" && value.State != "withdrawn" || value.Status != "" || value.LogoHash != "" || value.Source != "" || value.SourceEventID != "" || value.SourceCityID != "" || value.SourceScopeKey != "" {
			return value, errors.New("invalid: sponsor claim")
		}
	} else if value.RecordType == "intake" {
		marker = value.Status
		if value.State != "" || value.Status != "pending" || value.LogoHash != "" || value.Source != "" || value.SourceEventID != "" || value.SourceCityID != "" || value.SourceScopeKey != "" {
			return value, errors.New("invalid: sponsor intake")
		}
	} else if value.RecordType == "approval" {
		marker = value.Status
		if value.State != "" || value.Status != "approved" && value.Status != "suspended" || !hexKeyPattern.MatchString(value.LogoHash) || value.Source != "legacy-assignment" && value.Source != "self-claim" || !hexKeyPattern.MatchString(value.SourceEventID) || !uuidPattern.MatchString(value.SourceCityID) || !sizeOK(value.SourceScopeKey, 1, 640) {
			return value, errors.New("invalid: sponsor approval")
		}
	} else {
		return value, errors.New("invalid: sponsor catalogue record type")
	}
	d, de := uniqueTag(event, "d")
	indexed, ie := uniqueTag(event, "i")
	sponsor, pe := uniqueTag(event, "p")
	recordType, re := uniqueTag(event, "record")
	state, se := uniqueTag(event, "state")
	client, ce := uniqueTag(event, "client")
	prefix := sponsorCatalogueID + ":" + value.RecordType + ":" + value.SponsorPubkey + ":"
	if de != nil || ie != nil || pe != nil || re != nil || se != nil || ce != nil || !strings.HasPrefix(d, prefix) || !uuidPattern.MatchString(strings.TrimPrefix(d, prefix)) || indexed != sponsorCatalogueID || sponsor != value.SponsorPubkey || recordType != value.RecordType || state != marker || client != "bitcoinwalk.org" {
		return value, errors.New("invalid: sponsor catalogue tags")
	}
	previousCount, previousID, sourceCount, sourceID := 0, "", 0, ""
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			return value, errors.New("invalid: sponsor catalogue tag")
		}
		if tag[0] == "e" {
			if len(tag) != 4 || tag[2] != "" || tag[3] != "previous" && tag[3] != "source" {
				return value, errors.New("invalid: sponsor catalogue event reference")
			}
			if tag[3] == "previous" {
				previousCount++
				previousID = tag[1]
			} else {
				sourceCount++
				sourceID = tag[1]
			}
		} else if (tag[0] != "d" && tag[0] != "i" && tag[0] != "p" && tag[0] != "record" && tag[0] != "state" && tag[0] != "client") || len(tag) != 2 {
			return value, errors.New("restricted: unsupported sponsor catalogue tag")
		}
	}
	if (value.PreviousRevisionID == "") != (previousCount == 0) || previousCount > 1 || previousID != value.PreviousRevisionID || value.RecordType != "approval" && sourceCount != 0 || value.RecordType == "approval" && (sourceCount != 1 || sourceID != value.SourceEventID) {
		return value, errors.New("invalid: sponsor catalogue references")
	}
	return value, nil
}

func (p *organizerPolicy) latestSponsorCatalogue(author nostr.PubKey, recordType, sponsor string) *nostr.Event {
	var latest *nostr.Event
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{sponsorCatalogueKind}, Authors: []nostr.PubKey{author}, Tags: nostr.TagMap{"p": []string{sponsor}, "record": []string{recordType}}}, 10001) {
		copy := event
		if latest == nil || copy.CreatedAt > latest.CreatedAt || copy.CreatedAt == latest.CreatedAt && copy.ID.Hex() > latest.ID.Hex() {
			latest = &copy
		}
	}
	return latest
}

func (p *organizerPolicy) approvedSponsorLogo(pubkey, hash string) bool {
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{sponsorshipKind}, Authors: []nostr.PubKey{p.admin}}, 10001) {
		value, err := parseSponsorship(event)
		if err == nil && value.Mode == "sponsor" && value.SponsorPubkey == pubkey && value.LogoHash == hash {
			return true
		}
	}
	return false
}

func (p *organizerPolicy) checkSponsorCatalogue(event nostr.Event) error {
	value, err := parseSponsorCatalogue(event)
	if err != nil {
		return err
	}
	if (value.RecordType == "approval" || value.RecordType == "intake") && event.PubKey != p.admin {
		return errors.New("restricted: only the super-admin can manage sponsor catalogue review records")
	}
	if p.byID(event.ID.Hex()) != nil {
		return nil
	}
	current := p.latestSponsorCatalogue(event.PubKey, value.RecordType, value.SponsorPubkey)
	if current == nil && value.PreviousRevisionID != "" {
		return errors.New("invalid: first sponsor catalogue revision cannot reference a predecessor")
	}
	if value.RecordType == "claim" && current == nil && value.State == "withdrawn" {
		return errors.New("invalid: sponsor catalogue withdrawal requires an active predecessor")
	}
	if current != nil && (value.PreviousRevisionID != current.ID.Hex() || event.CreatedAt <= current.CreatedAt) {
		return errors.New("restricted: sponsor catalogue changed; reload before publishing")
	}
	if value.RecordType == "claim" {
		return nil
	}
	if value.RecordType == "intake" {
		return nil
	}
	sponsorKey, keyErr := nostr.PubKeyFromHex(value.SponsorPubkey)
	if keyErr != nil {
		return errors.New("invalid: sponsor catalogue identity")
	}
	if latestClaim := p.latestSponsorCatalogue(sponsorKey, "claim", value.SponsorPubkey); latestClaim != nil {
		claim, claimErr := parseSponsorCatalogue(*latestClaim)
		if claimErr != nil || claim.State == "withdrawn" {
			return errors.New("restricted: withdrawn sponsor catalogue profile cannot be approved")
		}
	}
	source := p.byID(value.SourceEventID)
	if source == nil {
		return errors.New("restricted: sponsor catalogue source is unavailable")
	}
	if value.Source == "legacy-assignment" {
		assignment, parseErr := parseSponsorship(*source)
		if parseErr != nil || source.PubKey != p.admin || assignment.Mode != "sponsor" || assignment.SponsorPubkey != value.SponsorPubkey || assignment.LogoHash != value.LogoHash || sponsorshipKey(assignment.Scope) != value.SourceScopeKey || assignment.Scope.CityID != value.SourceCityID || assignment.Website != value.Website {
			return errors.New("restricted: sponsor catalogue import does not match the signed assignment")
		}
		return nil
	}
	claim, parseErr := parseSponsorCatalogue(*source)
	latestClaim := p.latestSponsorCatalogue(source.PubKey, "claim", value.SponsorPubkey)
	if parseErr != nil || claim.RecordType != "claim" || source.PubKey.Hex() != value.SponsorPubkey || latestClaim == nil || latestClaim.ID != source.ID || claim.State != "active" || claim.DisplayName != value.DisplayName || claim.Website != value.Website || value.SourceCityID != sponsorCatalogueAssetCityID || value.SourceScopeKey != "catalogue:"+value.SponsorPubkey {
		return errors.New("restricted: sponsor catalogue approval does not match the sponsor claim and approved artwork")
	}
	return nil
}
