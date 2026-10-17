package chatstate

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestOrganizerGroupIsPrivateRestrictedAndClosed(t *testing.T) {
	admin, relay, member, outsider := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	path := filepath.Join(t.TempDir(), "chat.db")
	s, err := OpenSigned(path, nostr.GetPublicKey(admin), relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			s.Close()
		}
	})

	apply(t, s, event(t, admin, 9007, organizerGroupID))
	if err := s.VerifyRecovery(); err != nil {
		t.Fatal("partially provisioned organizer group must remain recoverable", err)
	}
	if err := s.Apply(event(t, outsider, 9021, organizerGroupID), nostr.GetPublicKey(outsider), now); err == nil || err.Error() != "restricted: invitation required" {
		t.Fatalf("outsider joined organizer group before metadata: %v", err)
	}
	preMetadata := 0
	if err := s.DeliverMetadata(nostr.GetPublicKey(outsider), nostr.Filter{Kinds: []nostr.Kind{39000}, Tags: nostr.TagMap{"d": {organizerGroupID}}}, func(nostr.Event) error { preMetadata++; return nil }); err != nil || preMetadata != 0 {
		t.Fatalf("reserved organizer group leaked before metadata: count=%d err=%v", preMetadata, err)
	}
	metadata := event(t, admin, 9002, organizerGroupID,
		nostr.Tag{"name", "BitcoinWalk Organizers"},
		nostr.Tag{"supported_kinds", "9"},
		nostr.Tag{"private"}, nostr.Tag{"restricted"}, nostr.Tag{"closed"},
	)
	apply(t, s, metadata)

	if err := s.Apply(event(t, outsider, 9021, organizerGroupID), nostr.GetPublicKey(outsider), now); err == nil || err.Error() != "restricted: invitation required" {
		t.Fatalf("outsider joined organizer group: %v", err)
	}
	apply(t, s, event(t, admin, 9000, organizerGroupID, nostr.Tag{"p", nostr.GetPublicKey(member).Hex()}))
	if err := s.CheckMember(organizerGroupID, nostr.GetPublicKey(member)); err != nil {
		t.Fatal(err)
	}

	visible := func(key nostr.PubKey) int {
		count := 0
		err := s.DeliverMetadata(key, nostr.Filter{Kinds: []nostr.Kind{39000}, Tags: nostr.TagMap{"d": {organizerGroupID}}}, func(e nostr.Event) error {
			flags := map[string]bool{}
			for _, tag := range e.Tags {
				flags[tag[0]] = true
			}
			if !flags["private"] || !flags["restricted"] || !flags["closed"] || flags["open"] {
				t.Fatalf("wrong organizer metadata: %v", e.Tags)
			}
			count++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	if visible(nostr.PubKey{}) != 0 || visible(nostr.GetPublicKey(outsider)) != 0 {
		t.Fatal("private organizer metadata leaked")
	}
	if visible(nostr.GetPublicKey(member)) != 1 {
		t.Fatal("member cannot discover organizer group")
	}

	if err := s.VerifyRecovery(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSigned(path, nostr.GetPublicKey(admin), relay)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(event(t, outsider, 9021, organizerGroupID), nostr.GetPublicKey(outsider), now); err == nil || err.Error() != "restricted: invitation required" {
		t.Fatalf("closed policy did not survive restart: %v", err)
	}
}

func TestOrganizerVisibilityExceptionIsNarrow(t *testing.T) {
	s, admin, _ := openTest(t)
	for _, bad := range []nostr.Event{
		event(t, admin, 9002, organizerGroupID, nostr.Tag{"private"}, nostr.Tag{"restricted"}),
		event(t, admin, 9002, organizerGroupID, nostr.Tag{"private"}, nostr.Tag{"restricted"}, nostr.Tag{"closed"}, nostr.Tag{"open"}),
		event(t, admin, 9002, "city-a", nostr.Tag{"private"}, nostr.Tag{"restricted"}, nostr.Tag{"closed"}),
		event(t, admin, 9002, "another-private-room", nostr.Tag{"private"}, nostr.Tag{"restricted"}, nostr.Tag{"closed"}),
	} {
		if err := s.Apply(bad, bad.PubKey, time.Now()); err == nil {
			t.Fatalf("accepted non-canonical private metadata: %v", bad.Tags)
		}
	}
	if err := s.Apply(event(t, admin, 9002, "city-a", nostr.Tag{"open"}), nostr.GetPublicKey(admin), now); err != nil {
		t.Fatal("public city metadata regressed", err)
	}
	if err := s.CheckMember("city-a", nostr.GetPublicKey(nostr.Generate())); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}
