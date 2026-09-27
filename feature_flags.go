package main

import (
	"bytes"
	"encoding/json"
	"errors"

	"fiatjaf.com/nostr"
)

const featureFlagsKind nostr.Kind = 30308
const featureFlagsID = "bitcoinwalk-feature-flags"

type featureFlags struct {
	PaidTierRegistration bool   `json:"paidTierRegistration"`
	PreviousRevisionID   string `json:"previousRevisionId,omitempty"`
}

func parseFeatureFlags(event nostr.Event) (featureFlags, error) {
	var flags featureFlags
	decoder := json.NewDecoder(bytes.NewBufferString(event.Content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&flags) != nil {
		return flags, errors.New("invalid: feature flags JSON required")
	}
	d, de := uniqueTag(event, "d")
	i, ie := uniqueTag(event, "i")
	client, ce := uniqueTag(event, "client")
	if de != nil || ie != nil || ce != nil || !workflowAddress(d, featureFlagsID) || i != featureFlagsID || client != "bitcoinwalk.org" {
		return flags, errors.New("invalid: feature flags tags")
	}
	previousTags, previousID := 0, ""
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			return flags, errors.New("invalid: malformed feature flag tag")
		}
		if tag[0] == "e" {
			if len(tag) != 4 || tag[2] != "" || tag[3] != "previous" {
				return flags, errors.New("invalid: feature flags previous reference")
			}
			previousTags++
			previousID = tag[1]
		} else if (tag[0] != "d" && tag[0] != "i" && tag[0] != "client") || len(tag) != 2 {
			return flags, errors.New("restricted: unsupported feature flag tag")
		}
	}
	if (flags.PreviousRevisionID == "") != (previousTags == 0) || previousTags > 1 || previousID != flags.PreviousRevisionID {
		return flags, errors.New("invalid: feature flags previous reference mismatch")
	}
	return flags, nil
}

func (p *organizerPolicy) checkFeatureFlags(event nostr.Event) error {
	if event.PubKey != p.admin {
		return errors.New("restricted: only the super-admin can change feature flags")
	}
	flags, err := parseFeatureFlags(event)
	if err != nil {
		return err
	}
	current := p.latestByIndexed(featureFlagsKind, featureFlagsID)
	if current == nil && flags.PreviousRevisionID != "" {
		return errors.New("invalid: first feature flags revision cannot reference a predecessor")
	}
	if current != nil && (flags.PreviousRevisionID != current.ID.Hex() || event.CreatedAt <= current.CreatedAt) {
		return errors.New("restricted: feature flags changed; reload before publishing")
	}
	return nil
}

func (p *organizerPolicy) paidRegistrationEnabled() bool {
	current := p.latestByIndexed(featureFlagsKind, featureFlagsID)
	if current == nil {
		return false
	}
	flags, err := parseFeatureFlags(*current)
	return err == nil && flags.PaidTierRegistration
}
