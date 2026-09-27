package chatstate

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

func TestSignedMetadataAtomicityAndPrivacy(t *testing.T) {
	admin, relay, user := nostr.Generate(), nostr.Generate(), nostr.Generate()
	path := filepath.Join(t.TempDir(), "signed.db")
	s, err := OpenSigned(path, nostr.GetPublicKey(admin), relay)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	apply(t, s, event(t, admin, 9007, "a"))
	read := func(key nostr.PubKey) map[nostr.Kind]nostr.Event {
		out := map[nostr.Kind]nostr.Event{}
		err := s.DeliverMetadata(key, nostr.Filter{Kinds: []nostr.Kind{39000, 39001, 39002, 39003}}, func(e nostr.Event) error {
			if !e.CheckID() || !e.VerifySignature() || e.PubKey != nostr.GetPublicKey(relay) {
				t.Fatal("invalid relay metadata")
			}
			out[e.Kind] = e
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if len(read(nostr.PubKey{})) != 3 {
		t.Fatal("anonymous roster leak")
	}
	before := read(nostr.GetPublicKey(admin))
	join := event(t, user, 9021, "a")
	apply(t, s, join)
	after := read(nostr.GetPublicKey(user))
	if len(after) != 4 || !nostr.IsOlder(before[39002], after[39002]) {
		t.Fatal("same-second metadata ordering")
	}
	// Confirm relay-generated admission, not an unsigned synthetic grant.
	err = s.db.View(func(tx *bbolt.Tx) error {
		b, _, err := group(tx, "a")
		if err != nil {
			return err
		}
		_, data := b.Bucket([]byte("moderation")).Cursor().Last()
		var e nostr.Event
		if err := json.Unmarshal(data, &e); err != nil {
			return err
		}
		if e.Kind != 9000 || e.PubKey != nostr.GetPublicKey(relay) || !e.VerifySignature() {
			t.Fatal("missing canonical admission")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A clock error during projection must roll back membership and log too.
	leave := event(t, user, 9022, "a")
	if err := s.Apply(leave, leave.PubKey, now.Add(-time.Second)); err == nil {
		t.Fatal("backwards projection accepted")
	}
	if err := s.CheckMember("a", leave.PubKey); err != nil {
		t.Fatal("failed projection changed membership")
	}
	apply(t, s, leave)
	if len(read(leave.PubKey)) != 3 {
		t.Fatal("former member reads roster")
	}
	self, _ := s.RelayIdentity()
	s.Close()
	if x, err := Open(path, nostr.GetPublicKey(admin)); err == nil {
		x.Close()
		t.Fatal("signed DB reopened unsigned")
	}
	if x, err := OpenSigned(path, nostr.GetPublicKey(admin), nostr.Generate()); err == nil {
		x.Close()
		t.Fatal("relay identity silently replaced")
	}
	s, err = OpenSigned(path, nostr.GetPublicKey(admin), relay)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.RelayIdentity()
	if got != self {
		t.Fatal("identity lost")
	}
	if read(nostr.GetPublicKey(admin))[39002].ID == after[39002].ID {
		t.Fatal("leave projection lost on restart")
	}
}

func TestSignedStoreRejectsImplicitMigration(t *testing.T) {
	s, admin, path := openTest(t)
	s.Close()
	if x, err := OpenSigned(path, nostr.GetPublicKey(admin), nostr.Generate()); err == nil {
		x.Close()
		t.Fatal("unsigned history silently migrated")
	}
	if x, err := OpenSigned(filepath.Join(t.TempDir(), "bad.db"), nostr.GetPublicKey(admin), admin); err == nil {
		x.Close()
		t.Fatal("user key used as relay identity")
	}
}
