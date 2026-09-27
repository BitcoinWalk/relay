package chatstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

// VerifyRecovery independently replays every original signed command in commit
// order into a disposable database and compares derived authority and message
// indexes. It never repairs or overwrites the source. A failure blocks staging
// startup. The temporary directory is private and removed when verification ends.
func (s *Store) VerifyRecovery() error {
	dir, err := os.MkdirTemp("", "bitcoinwalk-chat-recovery-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	replay, err := Open(filepath.Join(dir, "replay.db"), s.admin)
	if err != nil {
		return err
	}
	defer replay.Close()
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.db.View(func(tx *bbolt.Tx) error {
		root := tx.Bucket(groups)
		return root.ForEach(func(id, v []byte) error {
			if v != nil {
				return nil
			}
			b := root.Bucket(id)
			log := b.Bucket([]byte("log"))
			if log == nil {
				return errors.New("recovery: missing audit log")
			}
			err := log.ForEach(func(_ []byte, data []byte) error {
				var record Record
				if err := json.Unmarshal(data, &record); err != nil {
					return err
				}
				// Re-evaluate historical commands at their signing time. This
				// bypasses only today's freshness window, not signatures/authority.
				return replay.Apply(record.Event, record.Event.PubKey, time.Unix(int64(record.Event.CreatedAt), 0))
			})
			if err != nil {
				return errors.New("recovery: signed audit replay failed")
			}
			if err := replay.db.View(func(other *bbolt.Tx) error {
				derived := other.Bucket(groups).Bucket(id)
				if derived == nil {
					return errors.New("recovery: missing derived group")
				}
				if !bytes.Equal(b.Get([]byte("state")), derived.Get([]byte("state"))) {
					return errors.New("recovery: membership or ban state differs from audit")
				}
				for _, name := range []string{"log", "seen", "messages"} {
					a, c := b.Bucket([]byte(name)), derived.Bucket([]byte(name))
					if a == nil || c == nil {
						return errors.New("recovery: missing index")
					}
					left, right := a.Cursor(), c.Cursor()
					ak, av := left.First()
					ck, cv := right.First()
					for ak != nil || ck != nil {
						if !bytes.Equal(ak, ck) || !bytes.Equal(av, cv) {
							return errors.New("recovery: index differs from audit")
						}
						ak, av = left.Next()
						ck, cv = right.Next()
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if s.signer == nil {
				return nil
			}
			meta := b.Bucket([]byte("metadata"))
			ledger := b.Bucket([]byte("moderation"))
			if meta == nil || ledger == nil {
				return errors.New("recovery: missing signed projections")
			}
			self := nostr.GetPublicKey(*s.signer)
			_, state, err := group(tx, string(id))
			if err != nil {
				return err
			}
			for kind := 39000; kind <= 39003; kind++ {
				var e nostr.Event
				if err := json.Unmarshal(meta.Get([]byte(strconv.Itoa(kind))), &e); err != nil {
					return err
				}
				if e.Kind != nostr.Kind(kind) || e.PubKey != self || !e.CheckID() || !e.VerifySignature() || e.Tags.GetD() != string(id) {
					return errors.New("recovery: invalid signed metadata")
				}
				if kind == 39001 || kind == 39002 {
					seen := map[string]bool{}
					for _, t := range e.Tags {
						if len(t) == 0 || t[0] != "p" {
							continue
						}
						if len(t) < 2 || seen[t[1]] || !state.Members[t[1]] {
							return errors.New("recovery: metadata roster differs from authority")
						}
						seen[t[1]] = true
						if kind == 39001 {
							role := "moderator"
							if t[1] == s.admin.Hex() {
								role = "super-admin"
							}
							if !state.Moderators[t[1]] || len(t) != 3 || t[2] != role {
								return errors.New("recovery: metadata role differs from authority")
							}
						}
					}
					expected := len(state.Members)
					if kind == 39001 {
						expected = len(state.Moderators)
					}
					if len(seen) != expected {
						return errors.New("recovery: incomplete metadata roster")
					}
				}
			}
			return ledger.ForEach(func(_ []byte, data []byte) error {
				var e nostr.Event
				if err := json.Unmarshal(data, &e); err != nil {
					return err
				}
				if e.PubKey != self || !e.CheckID() || !e.VerifySignature() {
					return errors.New("recovery: invalid moderation projection")
				}
				source, err := tag(e, "e", true)
				if err != nil || b.Bucket([]byte("seen")).Get([]byte(source)) == nil {
					return errors.New("recovery: moderation source missing")
				}
				return nil
			})
		})
	})
}
