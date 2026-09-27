package chatstate

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

func (s *Store) RelayIdentity() (nostr.PubKey, bool) {
	if s.signer == nil {
		return nostr.PubKey{}, false
	}
	return nostr.GetPublicKey(*s.signer), true
}

// project is part of the SAME write transaction as membership and its audit
// record. Signing or persistence failure rolls the entire operation back.
func (s *Store) project(b *bbolt.Bucket, state State, request nostr.Event, now time.Time) error {
	if s.signer == nil {
		return nil
	}
	self := nostr.GetPublicKey(*s.signer)
	meta, err := b.CreateBucketIfNotExists([]byte("metadata"))
	if err != nil {
		return err
	}
	admins := nostr.Tags{{"d", state.ID}}
	members := nostr.Tags{{"d", state.ID}}
	keys := make([]string, 0, len(state.Members))
	for key := range state.Members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		members = append(members, nostr.Tag{"p", key})
		if state.Moderators[key] {
			role := "moderator"
			if key == s.admin.Hex() {
				role = "super-admin"
			}
			admins = append(admins, nostr.Tag{"p", key, role})
		}
	}
	name := state.Name
	if name == "" {
		name = "BitcoinWalk " + state.ID
	}
	info := nostr.Tags{{"d", state.ID}, {"name", name}, {"private"}, {"restricted"}, {"supported_kinds", "9"}}
	if state.About != "" {
		info = append(info, nostr.Tag{"about", state.About})
	}
	if state.Banner != "" {
		info = append(info, nostr.Tag{"banner", state.Banner})
	}
	sets := []nostr.Tags{
		info,
		admins, members,
		{{"d", state.ID}, {"role", "super-admin", "Create chats and manage chat roles"}, {"role", "moderator", "Remove, ban and unban chat members; no walk editing authority"}},
	}
	for i, tags := range sets {
		e := nostr.Event{Kind: nostr.Kind(39000 + i), PubKey: self, CreatedAt: nostr.Timestamp(now.Unix()), Tags: tags}
		key := []byte(strconv.Itoa(int(e.Kind)))
		var previous nostr.Event
		if data := meta.Get(key); data != nil {
			if err := json.Unmarshal(data, &previous); err != nil {
				return err
			}
			// Ignore our tie-break tag when checking if this projection changed.
			old := previous.Tags
			if len(old) > 0 && old[len(old)-1][0] == "bitcoinwalk-revision" {
				old = old[:len(old)-1]
			}
			a, _ := json.Marshal(old)
			c, _ := json.Marshal(tags)
			if bytes.Equal(a, c) {
				continue
			}
			if e.CreatedAt < previous.CreatedAt {
				return errors.New("error: clock moved backwards; retry when clock recovers")
			}
			if e.CreatedAt == previous.CreatedAt {
				// NIP-01 selects the lowest ID on equal timestamps. Keep time
				// honest rather than accumulating future-dated metadata. Bound
				// work; a very busy same-second group can retry next second.
				e.Tags = append(e.Tags, nostr.Tag{"bitcoinwalk-revision", "0"})
				found := false
				for n := 0; n < 4096; n++ {
					e.Tags[len(e.Tags)-1][1] = strconv.Itoa(n)
					id := e.GetID()
					if bytes.Compare(id[:], previous.ID[:]) < 0 {
						found = true
						break
					}
				}
				if !found {
					return errors.New("rate-limited: metadata revision budget; retry next second")
				}
			}
		}
		if err := e.Sign(*s.signer); err != nil {
			return err
		}
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := meta.Put(key, data); err != nil {
			return err
		}
	}
	ledger, err := b.CreateBucketIfNotExists([]byte("moderation"))
	if err != nil {
		return err
	}
	// Canonical effects use acceptance time and the relay signature. Keep the
	// original authorization in the immutable audit record and reference it.
	// Otherwise a delayed moderator event could sort before a newer join in
	// clients even though the relay accepted the removal afterwards.
	canonical := nostr.Event{Kind: request.Kind, CreatedAt: nostr.Timestamp(now.Unix()), Content: request.Content, Tags: append(append(nostr.Tags{}, request.Tags...), nostr.Tag{"e", request.ID.Hex()})}
	if err := canonical.Sign(*s.signer); err != nil {
		return err
	}
	events := []nostr.Event{canonical}
	if request.Kind == 9021 || request.Kind == 9022 {
		kind := nostr.Kind(9000)
		if request.Kind == 9022 {
			kind = 9001
		}
		e := nostr.Event{Kind: kind, CreatedAt: nostr.Timestamp(now.Unix()), Tags: nostr.Tags{{"h", state.ID}, {"p", request.PubKey.Hex()}, {"e", request.ID.Hex()}}, Content: "Self-service membership change"}
		if err := e.Sign(*s.signer); err != nil {
			return err
		}
		events = []nostr.Event{e}
	}
	if request.Kind == 9007 {
		e := nostr.Event{Kind: 9000, CreatedAt: nostr.Timestamp(now.Unix()), Tags: nostr.Tags{{"h", state.ID}, {"p", s.admin.Hex(), "super-admin"}, {"e", request.ID.Hex()}}, Content: "Initial chat administrator"}
		if err := e.Sign(*s.signer); err != nil {
			return err
		}
		events = append(events, e)
	}
	for _, e := range events {
		seq, err := ledger.NextSequence()
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, seq)
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := ledger.Put(key, data); err != nil {
			return err
		}
	}
	return nil
}

// DeliverMetadata exposes discovery metadata but never the membership roster
// to nonmembers. The callback runs inside the same revocation gate as messages.
func (s *Store) DeliverMetadata(key nostr.PubKey, filter nostr.Filter, deliver func(nostr.Event) error) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.db.View(func(tx *bbolt.Tx) error {
		limit := filter.Limit
		if limit <= 0 || limit > 200 {
			limit = 200
		}
		count := 0
		return tx.Bucket(groups).ForEach(func(id, v []byte) error {
			if v != nil || count >= limit {
				return nil
			}
			b, state, err := group(tx, string(id))
			if err != nil {
				return err
			}
			meta := b.Bucket([]byte("metadata"))
			if meta == nil {
				return nil
			}
			return meta.ForEach(func(_, data []byte) error {
				if count >= limit {
					return nil
				}
				var e nostr.Event
				if err := json.Unmarshal(data, &e); err != nil {
					return err
				}
				if e.Kind == 39002 && (!state.Members[key.Hex()] || state.Banned[key.Hex()]) {
					return nil
				}
				if !filter.Matches(e) {
					return nil
				}
				if err := deliver(e); err != nil {
					return err
				}
				count++
				return nil
			})
		})
	})
}

// DeliverMembershipStatus lets an authenticated user inspect only their own
// membership transitions, including after leaving. It reveals no chat messages
// or other members' records. This is how Armada determines joined/left state.
func (s *Store) DeliverMembershipStatus(key nostr.PubKey, f nostr.Filter, deliver func(nostr.Event) error) error {
	if key == (nostr.PubKey{}) || len(f.Tags["h"]) != 1 || len(f.Tags["p"]) != 1 || f.Tags["p"][0] != key.Hex() {
		return ErrDenied
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.db.View(func(tx *bbolt.Tx) error {
		b, _, err := group(tx, f.Tags["h"][0])
		if err != nil {
			return err
		}
		ledger := b.Bucket([]byte("moderation"))
		if ledger == nil {
			return nil
		}
		limit := f.Limit
		if limit <= 0 || limit > 200 {
			limit = 200
		}
		n := 0
		c := ledger.Cursor()
		for _, data := c.Last(); data != nil && n < limit; _, data = c.Prev() {
			var e nostr.Event
			if err := json.Unmarshal(data, &e); err != nil {
				return err
			}
			if e.Kind != 9000 && e.Kind != 9001 {
				continue
			}
			// The local unban extension does not itself restore membership.
			unban, _ := tag(e, "bitcoinwalk-unban", false)
			if unban != "" {
				continue
			}
			if f.Matches(e) {
				if err := deliver(e); err != nil {
					return err
				}
				n++
			}
		}
		return nil
	})
}
