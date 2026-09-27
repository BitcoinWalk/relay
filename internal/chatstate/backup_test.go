package chatstate

import (
	"errors"
	"fiatjaf.com/nostr"
	"path/filepath"
	"testing"
)

func TestBackupRestorePreservesBanMessagesAndIdentity(t *testing.T) {
	admin, key, user := nostr.Generate(), nostr.Generate(), nostr.Generate()
	s, err := OpenSigned(filepath.Join(t.TempDir(), "live.db"), nostr.GetPublicKey(admin), key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	apply(t, s, event(t, admin, 9007, "backup-test"))
	apply(t, s, event(t, user, 9021, "backup-test"))
	message := event(t, user, 9, "backup-test")
	apply(t, s, message)
	apply(t, s, event(t, admin, 9001, "backup-test", nostr.Tag{"p", nostr.GetPublicKey(user).Hex()}, nostr.Tag{"bitcoinwalk-ban", "true"}))
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.Backup(path); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(path); err == nil {
		t.Fatal("backup overwrote existing snapshot")
	}
	restored, err := OpenSigned(path, nostr.GetPublicKey(admin), key)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.VerifyRecovery(); err != nil {
		t.Fatal(err)
	}
	if err := restored.CheckMember("backup-test", nostr.GetPublicKey(user)); !errors.Is(err, ErrDenied) {
		t.Fatal("restored access", err)
	}
	join := event(t, user, 9021, "backup-test")
	join.Content = "fresh rejoin"
	join.Sign(user)
	if err := restored.Apply(join, join.PubKey, now); !errors.Is(err, ErrBanned) {
		t.Fatal("restored ban lost", err)
	}
	count := 0
	if err := restored.DeliverHistory("backup-test", nostr.GetPublicKey(admin), 20, func(e nostr.Event) error {
		if e.ID != message.ID {
			t.Fatal("message changed")
		}
		count++
		return nil
	}); err != nil || count != 1 {
		t.Fatal("restored history", count, err)
	}
	self, _ := restored.RelayIdentity()
	if self != nostr.GetPublicKey(key) {
		t.Fatal("restored identity changed")
	}
	metadata := func(store *Store) map[nostr.Kind]nostr.ID {
		out := map[nostr.Kind]nostr.ID{}
		if err := store.DeliverMetadata(nostr.GetPublicKey(admin), nostr.Filter{}, func(e nostr.Event) error { out[e.Kind] = e.ID; return nil }); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before, after := metadata(s), metadata(restored)
	if len(before) != 4 || len(after) != 4 {
		t.Fatal("missing projections")
	}
	for k, id := range before {
		if after[k] != id {
			t.Fatal("metadata changed on restore")
		}
	}
}
