package chatstate

import (
	"encoding/json"
	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
	"testing"
)

func TestRecoveryReplaysLegacyPrivateVisibilityWithoutAllowingNewWrites(t *testing.T) {
	s, admin, _ := openTest(t)
	legacy := event(t, admin, 9002, "city-a", nostr.Tag{"visibility", "private"})
	if err := s.apply(legacy, legacy.PubKey, now, true); err != nil {
		t.Fatal("prepare legacy signed history", err)
	}
	current := event(t, admin, 9002, "city-a", nostr.Tag{"visibility", "private"})
	if err := s.Apply(current, current.PubKey, now); err == nil {
		t.Fatal("live write accepted legacy private visibility")
	}
	if err := s.VerifyRecovery(); err != nil {
		t.Fatal("legacy signed history did not replay", err)
	}
}

func TestRecoveryReplaysLegacyPrivateFlagWithoutPersistingNewState(t *testing.T) {
	s, admin, _ := openTest(t)
	legacy := event(t, admin, 9002, "city-a", nostr.Tag{"private"})
	if err := s.apply(legacy, legacy.PubKey, now, true); err != nil {
		t.Fatal("prepare legacy signed history", err)
	}
	if err := s.VerifyRecovery(); err != nil {
		t.Fatal("legacy private flag changed durable state during replay", err)
	}
}

func TestRecoveryRejectsAuthorityTampering(t *testing.T) {
	s, _, _ := openTest(t)
	if err := s.VerifyRecovery(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Update(func(tx *bbolt.Tx) error {
		b, state, err := group(tx, "city-a")
		if err != nil {
			return err
		}
		state.Banned["unaudited"] = true
		data, _ := json.Marshal(state)
		return b.Put([]byte("state"), data)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyRecovery(); err == nil {
		t.Fatal("unaudited authority change accepted")
	}
}
