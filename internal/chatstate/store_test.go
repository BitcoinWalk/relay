package chatstate

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

var now = time.Unix(1800000000, 0)

func event(t *testing.T, key nostr.SecretKey, kind nostr.Kind, group string, extra ...nostr.Tag) nostr.Event {
	t.Helper()
	e := nostr.Event{Kind: kind, CreatedAt: nostr.Timestamp(now.Unix()), Content: "test", Tags: nostr.Tags{{"h", group}}}
	e.Tags = append(e.Tags, extra...)
	if err := e.Sign(key); err != nil {
		t.Fatal(err)
	}
	return e
}

func apply(t *testing.T, s *Store, e nostr.Event) {
	t.Helper()
	if err := s.Apply(e, e.PubKey, now); err != nil {
		t.Fatal(err)
	}
}

func openTest(t *testing.T) (*Store, nostr.SecretKey, string) {
	t.Helper()
	admin := nostr.Generate()
	path := filepath.Join(t.TempDir(), "chat.db")
	s, err := Open(path, nostr.GetPublicKey(admin))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	apply(t, s, event(t, admin, 9007, "city-a"))
	apply(t, s, event(t, admin, 9007, "city-b"))
	return s, admin, path
}

func TestMembershipIsolationAndDurableBan(t *testing.T) {
	s, admin, path := openTest(t)
	user := nostr.Generate()
	pk := nostr.GetPublicKey(user)
	outsider := nostr.GetPublicKey(nostr.Generate())
	join := event(t, user, 9021, "city-a")
	message := event(t, user, 9, "city-a")
	if err := s.Apply(message, pk, now); !errors.Is(err, ErrDenied) {
		t.Fatalf("nonmember write: %v", err)
	}
	apply(t, s, join)
	apply(t, s, message)
	for _, key := range []nostr.PubKey{{}, outsider} {
		if err := s.DeliverHistory("city-a", key, 20, func(nostr.Event) error { t.Fatal("leak"); return nil }); !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
	}
	count := 0
	if err := s.DeliverHistory("city-a", pk, 20, func(e nostr.Event) error {
		count++
		if e.ID != message.ID {
			t.Fatal("wrong message")
		}
		return nil
	}); err != nil || count != 1 {
		t.Fatalf("history %d %v", count, err)
	}
	if err := s.DeliverHistory("city-b", pk, 20, func(nostr.Event) error { return nil }); !errors.Is(err, ErrDenied) {
		t.Fatal("cross-city read", err)
	}
	if err := s.Apply(event(t, user, 9001, "city-a", nostr.Tag{"p", nostr.GetPublicKey(admin).Hex()}), pk, now); err == nil {
		t.Fatal("member moderation accepted")
	}
	ban := event(t, admin, 9001, "city-a", nostr.Tag{"p", pk.Hex()}, nostr.Tag{"bitcoinwalk-ban", "true"})
	apply(t, s, ban)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, nostr.GetPublicKey(admin))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	join.Content = "fresh join"
	join.Sign(user)
	if err := reopened.Apply(join, pk, now); !errors.Is(err, ErrBanned) {
		t.Fatal("ban did not survive restart", err)
	}
	if err := reopened.DeliverHistory("city-a", pk, 20, func(nostr.Event) error { t.Fatal("banned history leak"); return nil }); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if err := reopened.DeliverLive("city-a", pk, message.ID, func(nostr.Event) error { t.Fatal("banned live leak"); return nil }); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	message.Content = "after ban"
	message.Sign(user)
	if err := reopened.Apply(message, pk, now); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	apply(t, reopened, event(t, admin, 9000, "city-a", nostr.Tag{"p", pk.Hex()}, nostr.Tag{"bitcoinwalk-unban", "true"}))
	// Unban allows a new join; it does not itself restore membership.
	if err := reopened.DeliverHistory("city-a", pk, 20, func(nostr.Event) error { return nil }); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	apply(t, reopened, join)
}

func TestLeaveReplayAndValidation(t *testing.T) {
	s, admin, _ := openTest(t)
	key := nostr.Generate()
	pk := nostr.GetPublicKey(key)
	join := event(t, key, 9021, "city-a")
	if err := s.Apply(join, nostr.GetPublicKey(admin), now); err == nil {
		t.Fatal("auth mismatch")
	}
	bad := join
	bad.Content = "tampered"
	if err := s.Apply(bad, pk, now); err == nil {
		t.Fatal("tamper")
	}
	bad = event(t, key, 9021, "city-a", nostr.Tag{"h", "city-b"})
	if err := s.Apply(bad, pk, now); err == nil {
		t.Fatal("duplicate group")
	}
	if err := s.Apply(join, pk, now.Add(10*time.Minute)); err == nil {
		t.Fatal("stale")
	}
	apply(t, s, join)
	apply(t, s, event(t, key, 9022, "city-a"))
	if err := s.Apply(join, pk, now); err == nil {
		t.Fatal("replayed join restored membership")
	}
	join.Content = "fresh"
	join.Sign(key)
	apply(t, s, join)
	bad = event(t, key, 30303, "city-a")
	if err := s.Apply(bad, pk, now); err == nil {
		t.Fatal("chat allowed walk edits")
	}
	bad = event(t, key, 9, "city-a", nostr.Tag{"expiration", "1"})
	if err := s.Apply(bad, pk, now); err == nil {
		t.Fatal("expiry silently ignored")
	}
	// Rejected operations do not leave records or change membership.
	apply(t, s, event(t, key, 9, "city-a"))
}

func TestConcurrentJoinSingleCommit(t *testing.T) {
	s, _, _ := openTest(t)
	key := nostr.Generate()
	e := event(t, key, 9021, "city-a")
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Apply(e, e.PubKey, now) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("%d joins committed", successes.Load())
	}
}

func TestDeliveryAndRemovalAreSerialized(t *testing.T) {
	s, admin, _ := openTest(t)
	key := nostr.Generate()
	pk := nostr.GetPublicKey(key)
	apply(t, s, event(t, key, 9021, "city-a"))
	message := event(t, key, 9, "city-a")
	apply(t, s, message)
	entered, release := make(chan struct{}), make(chan struct{})
	delivered := make(chan error, 1)
	go func() {
		delivered <- s.DeliverLive("city-a", pk, message.ID, func(nostr.Event) error { close(entered); <-release; return nil })
	}()
	<-entered
	ban := event(t, admin, 9001, "city-a", nostr.Tag{"p", pk.Hex()}, nostr.Tag{"bitcoinwalk-ban", "true"})
	removed := make(chan error, 1)
	go func() { removed <- s.Apply(ban, ban.PubKey, now) }()
	close(release)
	if err := <-delivered; err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverLive("city-a", pk, message.ID, func(nostr.Event) error { t.Fatal("post-removal delivery"); return nil }); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}

func TestWrongAdministratorAndDeliveryPayload(t *testing.T) {
	s, admin, path := openTest(t)
	if err := s.DeliverLive("city-a", nostr.GetPublicKey(admin), event(t, admin, 9, "city-b").ID, func(nostr.Event) error { t.Fatal("unstored message delivered"); return nil }); err == nil {
		t.Fatal("missing event accepted")
	}
	s.Close()
	if reopened, err := Open(path, nostr.GetPublicKey(nostr.Generate())); err == nil {
		reopened.Close()
		t.Fatal("admin silently replaced")
	}
}

func TestModeratorScopeAndPrivilegeEscalation(t *testing.T) {
	s, admin, _ := openTest(t)
	mod, user := nostr.Generate(), nostr.Generate()
	mpk, upk := nostr.GetPublicKey(mod), nostr.GetPublicKey(user)
	apply(t, s, event(t, admin, 9000, "city-a", nostr.Tag{"p", mpk.Hex(), "moderator"}))
	apply(t, s, event(t, user, 9021, "city-a"))
	apply(t, s, event(t, user, 9021, "city-b"))
	if err := s.Apply(event(t, mod, 9000, "city-a", nostr.Tag{"p", upk.Hex(), "moderator"}), mpk, now); err == nil {
		t.Fatal("moderator elevated user")
	}
	if err := s.Apply(event(t, mod, 9001, "city-b", nostr.Tag{"p", upk.Hex()}, nostr.Tag{"bitcoinwalk-ban", "true"}), mpk, now); err == nil {
		t.Fatal("cross-city moderation")
	}
	apply(t, s, event(t, mod, 9001, "city-a", nostr.Tag{"p", upk.Hex()}, nostr.Tag{"bitcoinwalk-ban", "true"}))
	join := event(t, user, 9021, "city-a")
	join.Content = "rejoin"
	join.Sign(user)
	if err := s.Apply(join, upk, now); !errors.Is(err, ErrBanned) {
		t.Fatal(err)
	}
	// Leaving removes active moderator authority; rejoining does not restore it.
	apply(t, s, event(t, mod, 9022, "city-a"))
	apply(t, s, event(t, mod, 9021, "city-a"))
	if err := s.Apply(event(t, mod, 9000, "city-a", nostr.Tag{"p", upk.Hex()}, nostr.Tag{"bitcoinwalk-unban", "true"}), mpk, now); err == nil {
		t.Fatal("rejoin restored moderator role")
	}
}

func TestFilteredHistoryLimitCountsMatches(t *testing.T) {
	s, admin, _ := openTest(t)
	first := event(t, admin, 9, "city-a")
	apply(t, s, first)
	second := event(t, admin, 9, "city-a")
	second.Content = "newer"
	second.Sign(admin)
	apply(t, s, second)
	count := 0
	err := s.DeliverFilteredHistory("city-a", nostr.GetPublicKey(admin), nostr.Filter{IDs: []nostr.ID{first.ID}, Limit: 1}, func(e nostr.Event) error {
		count++
		if e.ID != first.ID {
			t.Fatal("wrong result")
		}
		return nil
	})
	if err != nil || count != 1 {
		t.Fatalf("filtered history: %d %v", count, err)
	}
}
