package chatstate

import (
	"fiatjaf.com/nostr"
	"path/filepath"
	"testing"
	"time"
)

func TestChatNameRecoveryAndPrivacy(t *testing.T) {
	admin, relay, user := nostr.Generate(), nostr.Generate(), nostr.Generate()
	path := filepath.Join(t.TempDir(), "chat.db")
	s, err := OpenSigned(path, nostr.GetPublicKey(admin), relay)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	apply(t, s, event(t, admin, 9007, "existing"))
	if err := s.VerifyRecovery(); err != nil {
		t.Fatal("legacy recovery", err)
	}
	rename := event(t, admin, 9002, "existing", nostr.Tag{"name", "chat"}, nostr.Tag{"private"}, nostr.Tag{"open"}, nostr.Tag{"visibility", "private"})
	apply(t, s, rename)
	for _, bad := range []nostr.Event{
		event(t, user, 9002, "existing", nostr.Tag{"name", "bad"}),
		event(t, admin, 9002, "existing", nostr.Tag{"public"}),
		event(t, admin, 9002, "existing", nostr.Tag{"closed"}),
		event(t, admin, 9002, "existing", nostr.Tag{"name", ""}),
	} {
		if err := s.Apply(bad, bad.PubKey, time.Now()); err == nil {
			t.Fatal("accepted forbidden edit")
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
	found := false
	err = s.DeliverMetadata(nostr.PubKey{}, nostr.Filter{Kinds: []nostr.Kind{39000}}, func(e nostr.Event) error {
		name, private := "", false
		for _, tag := range e.Tags {
			if tag[0] == "name" {
				name = tag[1]
			}
			if tag[0] == "private" {
				private = true
			}
		}
		if name != "chat" || !private {
			t.Fatal("name/privacy not persisted")
		}
		found = true
		return nil
	})
	if err != nil || !found {
		t.Fatal("metadata missing", err)
	}
}
