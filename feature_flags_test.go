package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
)

func featureFlagEvent(t *testing.T, sk nostr.SecretKey, enabled bool, previous string, offset int) nostr.Event {
	t.Helper()
	body, _ := json.Marshal(featureFlags{PaidTierRegistration: enabled, PreviousRevisionID: previous})
	tags := nostr.Tags{{"d", featureFlagsID + fmt.Sprintf(":00000000-0000-4000-8000-%012d", offset)}, {"i", featureFlagsID}, {"client", "bitcoinwalk.org"}}
	if previous != "" {
		tags = append(tags, nostr.Tag{"e", previous, "", "previous"})
	}
	event := nostr.Event{Kind: featureFlagsKind, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Tags: tags, Content: string(body)}
	if err := event.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestFeatureFlagsAuthorityAndDefault(t *testing.T) {
	admin, other := nostr.Generate(), nostr.Generate()
	adminPK := nostr.GetPublicKey(admin)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	policy := enableOrganizers(relay, db, adminPK)
	unitOrganizerPolicy(relay, policy)
	if policy.paidRegistrationEnabled() {
		t.Fatal("paid registration must fail closed")
	}
	deny(t, relay, featureFlagEvent(t, other, true, "", 0))
	first := featureFlagEvent(t, admin, true, "", 0)
	accept(t, relay, first)
	if !policy.paidRegistrationEnabled() {
		t.Fatal("signed flag was not enabled")
	}
}
