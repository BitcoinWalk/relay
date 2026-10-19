package chatstate

import (
	"fiatjaf.com/nostr"
	"path/filepath"
	"testing"
	"time"
)

func TestMultiHistoryPublicReadAndPrivateIsolation(t *testing.T) {
	base := now
	defer func() { now = base }()
	admin, user := nostr.Generate(), nostr.Generate()
	s, err := OpenSigned(filepath.Join(t.TempDir(), "chat.db"), nostr.GetPublicKey(admin), nostr.Generate())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"a", "b", "c"} {
		apply(t, s, event(t, admin, 9007, id))
		apply(t, s, event(t, admin, 9, id))
	}
	apply(t, s, event(t, admin, 9007, organizerGroupID))
	apply(t, s, event(t, admin, 9002, organizerGroupID,
		nostr.Tag{"name", "BitcoinWalk Organizers"},
		nostr.Tag{"supported_kinds", "9"},
		nostr.Tag{"private"}, nostr.Tag{"restricted"}, nostr.Tag{"closed"},
	))
	apply(t, s, event(t, admin, 9, organizerGroupID))
	for _, id := range []string{"a", "c"} {
		now = now.Add(time.Second)
		apply(t, s, event(t, user, 9021, id))
	}
	f := nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": []string{"b", "a", "c", organizerGroupID, "missing"}}, Limit: 1}
	read := func(want int) {
		t.Helper()
		count := 0
		err := s.DeliverMultiHistory(nostr.GetPublicKey(user), f, func(e nostr.Event) error {
			for _, tag := range e.Tags {
				if tag[0] == "h" && tag[1] == organizerGroupID {
					t.Fatal("private organizer history leaked")
				}
			}
			count++
			return nil
		})
		if err != nil || count != want {
			t.Fatalf("count=%d want=%d err=%v", count, want, err)
		}
	}
	read(1)
	f.Limit = 200
	read(3)
	now = now.Add(time.Second)
	apply(t, s, event(t, user, 9022, "a"))
	read(3)
	now = now.Add(time.Second)
	apply(t, s, event(t, user, 9022, "c"))
	read(3)

	count := 0
	if err := s.DeliverMultiHistory(nostr.PubKey{}, f, func(nostr.Event) error { count++; return nil }); err != nil || count != 3 {
		t.Fatalf("anonymous public history count=%d err=%v", count, err)
	}
	apply(t, s, event(t, admin, 9000, organizerGroupID, nostr.Tag{"p", nostr.GetPublicKey(user).Hex()}))
	count = 0
	if err := s.DeliverMultiHistory(nostr.GetPublicKey(user), f, func(nostr.Event) error { count++; return nil }); err != nil || count != 4 {
		t.Fatalf("organizer history count=%d err=%v", count, err)
	}
}
