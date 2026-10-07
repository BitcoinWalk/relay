package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"

	"fiatjaf.com/nostr"
)

const cityBrandKind nostr.Kind = 30312

type cityBrandBinding struct {
	Version     int    `json:"version"`
	CityID      string `json:"cityId"`
	Sequence    uint64 `json:"sequence"`
	Previous    string `json:"previous"`
	Action      string `json:"action"`
	BrandPubkey string `json:"brandPubkey"`
}

func parseCityBrand(event nostr.Event) (cityBrandBinding, error) {
	var binding cityBrandBinding
	decoder := json.NewDecoder(bytes.NewBufferString(event.Content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&binding) != nil || decoder.Decode(new(any)) != io.EOF {
		return binding, errors.New("invalid: city identity JSON required")
	}
	canonical, _ := json.Marshal(binding)
	if event.Content != string(canonical) || binding.Version != 1 || !uuidPattern.MatchString(binding.CityID) ||
		!hexKeyPattern.MatchString(binding.BrandPubkey) || binding.Previous != "" && !hexKeyPattern.MatchString(binding.Previous) ||
		!slices.Contains([]string{"activate", "replace", "revoke"}, binding.Action) {
		return binding, errors.New("invalid: city identity binding")
	}
	expected := nostr.Tags{{"d", fmt.Sprintf("%s:%d", binding.CityID, binding.Sequence)}, {"i", binding.CityID}, {"t", "bitcoinwalk-city-brand-v1"}}
	if !slices.EqualFunc(event.Tags, expected, func(a, b nostr.Tag) bool { return slices.Equal(a, b) }) {
		return binding, errors.New("invalid: city identity tags")
	}
	return binding, nil
}

func (p *organizerPolicy) cityBrandHistory(cityID string) ([]nostr.Event, error) {
	var events []nostr.Event
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{cityBrandKind}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"i": []string{cityID}}}, 10001) {
		events = append(events, event)
		if len(events) > 10000 {
			return nil, errors.New("restricted: city identity history exceeds the safety limit")
		}
	}
	sort.Slice(events, func(i, j int) bool {
		a, aErr := parseCityBrand(events[i])
		b, bErr := parseCityBrand(events[j])
		if aErr != nil || bErr != nil {
			return events[i].ID.Hex() < events[j].ID.Hex()
		}
		return a.Sequence < b.Sequence
	})
	var previous *nostr.Event
	for i := range events {
		binding, err := parseCityBrand(events[i])
		if err != nil || binding.CityID != cityID {
			return nil, errors.New("restricted: invalid retained city identity history")
		}
		if err := validateCityBrandSuccessor(binding, previous); err != nil {
			return nil, err
		}
		previous = &events[i]
	}
	return events, nil
}

func validateCityBrandSuccessor(binding cityBrandBinding, previous *nostr.Event) error {
	if previous == nil {
		if binding.Sequence != 0 || binding.Previous != "" || binding.Action != "activate" {
			return errors.New("restricted: missing city identity predecessor")
		}
		return nil
	}
	prior, err := parseCityBrand(*previous)
	if err != nil || binding.CityID != prior.CityID || binding.Sequence != prior.Sequence+1 || binding.Previous != previous.ID.Hex() {
		return errors.New("restricted: stale city identity predecessor")
	}
	if binding.Action == "activate" || binding.Action == "replace" && binding.BrandPubkey == prior.BrandPubkey ||
		binding.Action == "revoke" && (prior.Action == "revoke" || binding.BrandPubkey != prior.BrandPubkey) {
		return errors.New("restricted: invalid city identity transition")
	}
	return nil
}

func (p *organizerPolicy) checkCityBrand(event nostr.Event) error {
	if event.PubKey != p.admin {
		return errors.New("restricted: only the super-admin can attest an official city identity")
	}
	binding, err := parseCityBrand(event)
	if err != nil {
		return err
	}
	if p.byID(event.ID.Hex()) != nil {
		return nil
	}
	grant, _, err := p.grant(binding.CityID)
	if err != nil || grant == nil {
		return errors.New("restricted: city identity requires an existing city")
	}
	history, err := p.cityBrandHistory(binding.CityID)
	if err != nil {
		return err
	}
	var previous *nostr.Event
	if len(history) > 0 {
		previous = &history[len(history)-1]
	}
	if err := validateCityBrandSuccessor(binding, previous); err != nil {
		return err
	}
	if previous != nil && event.CreatedAt <= previous.CreatedAt {
		return errors.New("restricted: city identity successor must be newer")
	}
	if binding.Action != "revoke" {
		decision := p.latestDecision(binding.CityID)
		var value cityDecision
		if decision == nil || json.Unmarshal([]byte(decision.Content), &value) != nil || value.Status != "approved" {
			return errors.New("restricted: city identity requires current city approval")
		}
		if status := p.moderationStatus(binding.CityID, "city:"+binding.CityID); status != "" && status != "active" {
			return errors.New("restricted: suspended city cannot activate or replace its identity")
		}
		if binding.BrandPubkey == p.admin.Hex() || binding.BrandPubkey == grant.CreatorPubkey {
			return errors.New("restricted: use a separate city identity")
		}
	}
	return nil
}
