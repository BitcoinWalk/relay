package chatstate

import (
	"fiatjaf.com/nostr"
	"path/filepath"
	"testing"
)

func TestMultiHistoryIsolationAndTotalLimit(t *testing.T) {
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
	for _, id := range []string{"a", "c"} {
		apply(t, s, event(t, user, 9021, id))
	}
	f := nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": []string{"b", "a", "c", "missing"}}, Limit: 1}
	read := func(want int) {
		t.Helper()
		count := 0
		err := s.DeliverMultiHistory(nostr.GetPublicKey(user), f, func(e nostr.Event) error {
			for _, tag := range e.Tags {
				if tag[0] == "h" && tag[1] == "b" {
					t.Fatal("unjoined history leaked")
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
	read(2)
	apply(t, s, event(t, user, 9022, "a"))
	read(1)
	apply(t, s, event(t, user, 9022, "c"))
	read(0)
}
