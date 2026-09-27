package chatstate

import (
	"fiatjaf.com/nostr"
	"path/filepath"
	"testing"
	"time"
)

func TestArmadaSettingsBanner(t *testing.T) {
	admin, relay, member := nostr.Generate(), nostr.Generate(), nostr.Generate()
	path := filepath.Join(t.TempDir(), "chat.db")
	s, err := OpenSigned(path, nostr.GetPublicKey(admin), relay)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	apply(t, s, event(t, admin, 9007, "austin"))
	settings := func(banner string) nostr.Event {
		return event(t, admin, 9002, "austin", nostr.Tag{"name", "chat"}, nostr.Tag{"about", ""}, nostr.Tag{"banner", banner}, nostr.Tag{"private"}, nostr.Tag{"visibility", "private"}, nostr.Tag{"open"})
	}
	// Exact settings shape sent by Armada, including an empty banner.
	apply(t, s, settings(""))
	apply(t, s, settings("https://example.com/banner.png"))
	for _, bad := range []nostr.Event{
		settings("javascript:alert(1)"), settings("http://example.com/a"), settings("https://user:pass@example.com/a"),
		event(t, admin, 9002, "austin", nostr.Tag{"banner"}),
		event(t, admin, 9002, "austin", nostr.Tag{"banner", ""}, nostr.Tag{"banner", ""}),
		event(t, member, 9002, "austin", nostr.Tag{"banner", ""}),
		event(t, admin, 9002, "austin", nostr.Tag{"banner", ""}, nostr.Tag{"public"}),
		event(t, admin, 9002, "austin", nostr.Tag{"banner", ""}, nostr.Tag{"closed"}),
		event(t, admin, 9007, "other", nostr.Tag{"banner", ""}),
	} {
		if s.Apply(bad, bad.PubKey, time.Now()) == nil {
			t.Fatal("accepted invalid or unauthorized metadata")
		}
	}
	check := func(want string) {
		t.Helper()
		found := false
		err := s.DeliverMetadata(nostr.PubKey{}, nostr.Filter{Kinds: []nostr.Kind{39000}}, func(e nostr.Event) error {
			name, banner, private, closed := "", "", false, false
			for _, tag := range e.Tags {
				switch tag[0] {
				case "name":
					name = tag[1]
				case "banner":
					banner = tag[1]
				case "private":
					private = true
				case "closed":
					closed = true
				}
			}
			if name != "chat" || banner != want || !private || closed {
				t.Fatalf("incorrect metadata: %v", e.Tags)
			}
			found = true
			return nil
		})
		if err != nil || !found {
			t.Fatalf("metadata: %v", err)
		}
	}
	if err := s.VerifyRecovery(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenSigned(path, nostr.GetPublicKey(admin), relay)
	if err != nil {
		t.Fatal(err)
	}
	check("https://example.com/banner.png")
	// Different about avoids generating a duplicate of the earlier empty update.
	apply(t, s, event(t, admin, 9002, "austin", nostr.Tag{"banner", ""}, nostr.Tag{"about", "cleared"}))
	check("")
	apply(t, s, event(t, member, 9021, "austin")) // open joining remains allowed
	if err := s.VerifyRecovery(); err != nil {
		t.Fatal(err)
	}
}
