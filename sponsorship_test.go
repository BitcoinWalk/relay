package main

import (
	"encoding/json"
	"fiatjaf.com/nostr"
	"fmt"
	"path/filepath"
	"testing"
)

func sponsorshipEvent(t *testing.T, sk nostr.SecretKey, mode, previous string, offset int) nostr.Event {
	t.Helper()
	value := sponsorship{Version: 1, Scope: sponsorshipScope{Type: "city", CityID: cityA}, Mode: mode, PreviousRevisionID: previous}
	if mode == "sponsor" {
		value.SponsorPubkey = nostr.GetPublicKey(nostr.Generate()).Hex()
		value.Website = "https://sponsor.example/"
	}
	body, _ := json.Marshal(value)
	key := sponsorshipKey(value.Scope)
	tags := nostr.Tags{{"d", sponsorshipID + ":" + key + fmt.Sprintf(":00000000-0000-4000-8000-%012d", offset)}, {"i", sponsorshipID}, {"scope", key}, {"mode", mode}, {"client", "bitcoinwalk.org"}}
	if previous != "" {
		tags = append(tags, nostr.Tag{"e", previous, "", "previous"})
	}
	event := nostr.Event{Kind: sponsorshipKind, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Tags: tags, Content: string(body)}
	if err := event.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return event
}
func TestSponsorshipAuthorityValidationAndSuccessor(t *testing.T) {
	admin, other := nostr.Generate(), nostr.Generate()
	adminPK := nostr.GetPublicKey(admin)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	policy := enableOrganizers(relay, db, adminPK)
	unitOrganizerPolicy(relay, policy)
	deny(t, relay, sponsorshipEvent(t, other, "empty", "", 0))
	first := sponsorshipEvent(t, admin, "sponsor", "", 0)
	accept(t, relay, first)
	deny(t, relay, sponsorshipEvent(t, admin, "hidden", "", 1))
	second := sponsorshipEvent(t, admin, "hidden", first.ID.Hex(), 2)
	accept(t, relay, second)
	accept(t, relay, second)
}

func TestSponsorshipLogoVersionBinding(t *testing.T) {
	for _, tc := range []struct {
		name       string
		version    int
		mode, hash string
		valid      bool
	}{
		{"legacy", 1, "sponsor", "", true},
		{"approved", 2, "sponsor", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true},
		{"removed", 2, "sponsor", "", true},
		{"legacy-logo", 1, "sponsor", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"hidden-logo", 2, "hidden", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"url", 2, "sponsor", "https://example.com/logo.png", false},
		{"unknown-version", 3, "sponsor", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := sponsorshipEvent(t, nostr.Generate(), tc.mode, "", 0)
			var value sponsorship
			if err := json.Unmarshal([]byte(event.Content), &value); err != nil {
				t.Fatal(err)
			}
			value.Version = tc.version
			value.LogoHash = tc.hash
			body, _ := json.Marshal(value)
			event.Content = string(body)
			_, err := parseSponsorship(event)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
