// Package chatstate is the durable, deny-by-default core for the chat pilot.
// It is not a complete NIP-29 relay: transport authentication, relay-signed
// moderation projections and metadata are integration responsibilities.
package chatstate

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

var groups = []byte("chat-groups-v1")
var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

var ErrDenied = errors.New("restricted: current chat membership required")
var ErrBanned = errors.New("restricted: banned from this chat")

type State struct {
	ID         string          `json:"id"`
	Members    map[string]bool `json:"members"`
	Moderators map[string]bool `json:"moderators"`
	Banned     map[string]bool `json:"banned"`
	Name       string          `json:"name,omitempty"`
	About      string          `json:"about,omitempty"`
	Banner     string          `json:"banner,omitempty"`
}

type Record struct {
	Sequence uint64      `json:"sequence"`
	Action   string      `json:"action"`
	Event    nostr.Event `json:"event"`
}

type Store struct {
	db     *bbolt.DB
	admin  nostr.PubKey
	signer *nostr.SecretKey
	// Delivery holds RLock through its callback. Membership changes take Lock,
	// so after a removal returns no new delivery to the removed key can start.
	gate sync.RWMutex
}

func Open(path string, admin nostr.PubKey) (*Store, error) {
	return open(path, admin, nil)
}

// OpenSigned binds a separate relay identity to this database. The caller must
// load its secret securely; it is never stored in the database or printed.
func OpenSigned(path string, admin nostr.PubKey, relayKey nostr.SecretKey) (*Store, error) {
	if nostr.GetPublicKey(relayKey) == admin {
		return nil, errors.New("relay key must differ from administrator")
	}
	return open(path, admin, &relayKey)
}

func open(path string, admin nostr.PubKey, signer *nostr.SecretKey) (*Store, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(groups)
		if err != nil {
			return err
		}
		if old := b.Get([]byte("admin")); old != nil && string(old) != admin.Hex() {
			return errors.New("chat database belongs to another administrator")
		}
		if signer == nil && b.Get([]byte("relay-self")) != nil {
			return errors.New("signed chat store requires its relay key")
		}
		if signer != nil {
			self := nostr.GetPublicKey(*signer).Hex()
			if old := b.Get([]byte("relay-self")); old != nil && string(old) != self {
				return errors.New("relay identity does not match database")
			}
			if b.Get([]byte("relay-self")) == nil {
				// Existing unsigned pilot data needs an explicit migration, not
				// an implicit metadata-only conversion without moderation history.
				if err := b.ForEach(func(_ []byte, v []byte) error {
					if v == nil {
						return errors.New("cannot enable signing on populated unsigned store")
					}
					return nil
				}); err != nil {
					return err
				}
			}
			if err := b.Put([]byte("relay-self"), []byte(self)); err != nil {
				return err
			}
		}
		return b.Put([]byte("admin"), []byte(admin.Hex()))
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, admin: admin, signer: signer}, nil
}

func (s *Store) Close() error {
	s.gate.Lock()
	defer s.gate.Unlock()
	return s.db.Close()
}

func tag(e nostr.Event, name string, required bool) (string, error) {
	value, found := "", false
	for _, t := range e.Tags {
		if len(t) == 0 || t[0] != name {
			continue
		}
		if found || len(t) != 2 || t[1] == "" {
			return "", fmt.Errorf("invalid: ambiguous %s tag", name)
		}
		value, found = t[1], true
	}
	if required && !found {
		return "", fmt.Errorf("invalid: missing %s tag", name)
	}
	return value, nil
}

func targetTag(e nostr.Event) (string, string, error) {
	key, role := "", ""
	for _, t := range e.Tags {
		if len(t) == 0 || t[0] != "p" {
			continue
		}
		if key != "" || len(t) < 2 || len(t) > 3 || t[1] == "" {
			return "", "", errors.New("invalid: ambiguous target")
		}
		key = t[1]
		if len(t) == 3 {
			if e.Kind != 9000 || t[2] != "moderator" {
				return "", "", errors.New("invalid: unsupported role")
			}
			role = t[2]
		}
	}
	if key == "" {
		return "", "", errors.New("invalid: missing target")
	}
	return key, role, nil
}

func group(tx *bbolt.Tx, id string) (*bbolt.Bucket, State, error) {
	b := tx.Bucket(groups).Bucket([]byte(id))
	var state State
	if b == nil {
		return nil, state, errors.New("restricted: unknown chat")
	}
	if err := json.Unmarshal(b.Get([]byte("state")), &state); err != nil {
		return nil, state, err
	}
	return b, state, nil
}

func save(b *bbolt.Bucket, state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return b.Put([]byte("state"), data)
}

// Apply requires the transport-verified NIP-42 identity, never a caller-supplied
// claim from an HTTP body. Replay detection, authorization, event persistence
// and membership/ban changes occur in one transaction.
// Pilot extension: 9001 + bitcoinwalk-ban=true bans; 9000 +
// bitcoinwalk-unban=true clears a ban without adding membership. These are
// explicit local policies, not standardized NIP-29 ban operations.
func (s *Store) Apply(e nostr.Event, authenticated nostr.PubKey, now time.Time) error {
	if e.PubKey != authenticated {
		return errors.New("auth-required: authenticate as author")
	}
	if !e.CheckID() || !e.VerifySignature() {
		return errors.New("invalid: event signature or ID")
	}
	if int64(e.CreatedAt) < now.Unix()-300 || int64(e.CreatedAt) > now.Unix()+60 {
		return errors.New("invalid: event timestamp outside pilot window")
	}
	if len(e.Content) > 5000 || len(e.Tags) > 32 {
		return errors.New("invalid: event too large")
	}
	id, err := tag(e, "h", true)
	if err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return errors.New("invalid: chat ID")
	}
	// Do not silently ignore unsupported authority, expiry or reference tags.
	for _, t := range e.Tags {
		if len(t) == 0 {
			return errors.New("invalid: empty tag")
		}
		if len(t) > 3 {
			return errors.New("invalid: pilot tag too large")
		}
		for _, value := range t {
			if len(value) > 512 {
				return errors.New("invalid: pilot tag value too large")
			}
		}
		switch t[0] {
		case "name", "about", "banner":
			if e.Kind != 9002 || len(t) != 2 {
				return errors.New("invalid: metadata tag")
			}
			count := 0
			for _, other := range e.Tags {
				if len(other) > 0 && other[0] == t[0] {
					count++
				}
			}
			if count != 1 {
				return errors.New("invalid: duplicate metadata tag")
			}
			if t[0] == "banner" && t[1] != "" {
				u, err := url.Parse(t[1])
				if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
					return errors.New("invalid: banner must be empty or an HTTPS URL")
				}
			}
		case "private", "open", "restricted":
			if e.Kind != 9002 || len(t) != 1 {
				return errors.New("invalid: metadata flag")
			}
		case "visibility":
			if e.Kind != 9002 || len(t) != 2 || t[1] != "private" {
				return errors.New("restricted: members-only visibility required")
			}
		case "h":
		case "client":
			if _, err := tag(e, "client", false); err != nil {
				return err
			}
		case "p":
			if e.Kind != 9000 && e.Kind != 9001 {
				return errors.New("invalid: unexpected target")
			}
		case "bitcoinwalk-ban":
			if e.Kind != 9001 {
				return errors.New("invalid: unexpected ban")
			}
		case "bitcoinwalk-unban":
			if e.Kind != 9000 {
				return errors.New("invalid: unexpected unban")
			}
		default:
			return errors.New("invalid: unsupported pilot tag")
		}
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		root := tx.Bucket(groups)
		if e.Kind == 9007 {
			if e.PubKey != s.admin {
				return errors.New("restricted: only administrator can create chats")
			}
			if root.Bucket([]byte(id)) != nil {
				return errors.New("duplicate: chat exists")
			}
			b, err := root.CreateBucket([]byte(id))
			if err != nil {
				return err
			}
			for _, name := range []string{"log", "seen", "messages"} {
				if _, err := b.CreateBucket([]byte(name)); err != nil {
					return err
				}
			}
			state := State{ID: id, Members: map[string]bool{e.PubKey.Hex(): true}, Moderators: map[string]bool{e.PubKey.Hex(): true}, Banned: map[string]bool{}}
			if err := save(b, state); err != nil {
				return err
			}
			if err := appendRecord(b, "create", e); err != nil {
				return err
			}
			return s.project(b, state, e, now)
		}
		b, state, err := group(tx, id)
		if err != nil {
			return err
		}
		if b.Bucket([]byte("seen")).Get([]byte(e.ID.Hex())) != nil {
			return errors.New("duplicate: event already processed")
		}
		author := e.PubKey.Hex()
		action := ""
		switch e.Kind {
		case 9002:
			if e.PubKey != s.admin {
				return errors.New("restricted: administrator required to edit chat metadata")
			}
			for _, t := range e.Tags {
				if t[0] == "name" {
					if strings.TrimSpace(t[1]) == "" || len(t[1]) > 80 {
						return errors.New("invalid: name must contain 1-80 bytes")
					}
					state.Name = strings.TrimSpace(t[1])
				}
				if t[0] == "about" {
					state.About = t[1]
				}
				if t[0] == "banner" {
					state.Banner = t[1]
				}
			}
			action = "metadata"
		case 9021:
			if state.Banned[author] {
				return ErrBanned
			}
			if state.Members[author] {
				return errors.New("duplicate: already a member")
			}
			state.Members[author] = true
			action = "join"
		case 9022:
			if !state.Members[author] {
				return ErrDenied
			}
			delete(state.Members, author)
			delete(state.Moderators, author)
			action = "leave"
		case 9000, 9001:
			if e.PubKey != s.admin && !(state.Members[author] && state.Moderators[author] && !state.Banned[author]) {
				return errors.New("restricted: chat moderator required")
			}
			target, role, err := targetTag(e)
			if err != nil {
				return err
			}
			key, err := nostr.PubKeyFromHex(target)
			if err != nil || key.Hex() != target {
				return errors.New("invalid: target key")
			}
			if key == s.admin {
				return errors.New("restricted: administrator cannot be removed or reassigned")
			}
			if state.Moderators[target] && e.PubKey != s.admin {
				return errors.New("restricted: moderator target requires administrator")
			}
			if e.Kind == 9000 {
				unban, err := tag(e, "bitcoinwalk-unban", false)
				if err != nil {
					return err
				}
				if unban != "" {
					if unban != "true" || role != "" {
						return errors.New("invalid: unban must be explicit and carry no role")
					}
					delete(state.Banned, target)
					action = "unban"
				} else {
					if e.PubKey != s.admin {
						return errors.New("restricted: only administrator can grant roles")
					}
					if state.Banned[target] {
						return ErrBanned
					}
					state.Members[target] = true
					delete(state.Moderators, target)
					if role == "moderator" {
						state.Moderators[target] = true
					}
					action = "grant"
				}
			} else {
				ban, err := tag(e, "bitcoinwalk-ban", false)
				if err != nil {
					return err
				}
				if ban != "" && ban != "true" {
					return errors.New("invalid: ban must be true")
				}
				delete(state.Members, target)
				delete(state.Moderators, target)
				action = "remove"
				if ban == "true" {
					state.Banned[target] = true
					action = "ban"
				}
			}
		case 9:
			if !state.Members[author] || state.Banned[author] {
				return ErrDenied
			}
			action = "message"
		default:
			return errors.New("restricted: unsupported chat pilot kind")
		}
		if err := save(b, state); err != nil {
			return err
		}
		if err := appendRecord(b, action, e); err != nil {
			return err
		}
		if action != "message" {
			return s.project(b, state, e, now)
		}
		return nil
	})
}

func appendRecord(b *bbolt.Bucket, action string, e nostr.Event) error {
	log := b.Bucket([]byte("log"))
	seq, err := log.NextSequence()
	if err != nil {
		return err
	}
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, seq)
	data, err := json.Marshal(Record{Sequence: seq, Action: action, Event: e})
	if err != nil {
		return err
	}
	if err := log.Put(key, data); err != nil {
		return err
	}
	if err := b.Bucket([]byte("seen")).Put([]byte(e.ID.Hex()), key); err != nil {
		return err
	}
	if action == "message" {
		return b.Bucket([]byte("messages")).Put(key, data)
	}
	return nil
}

// DeliverHistory performs a fresh membership check and delivers at most 200
// messages under a read gate. The callback must use bounded transport writes,
// must not call back into Store and must not retain events for later delivery.
// This prevents a snapshot obtained before a ban being delivered after it.
func (s *Store) DeliverHistory(id string, authenticated nostr.PubKey, limit int, deliver func(nostr.Event) error) error {
	return s.deliverHistory(id, authenticated, limit, func(nostr.Event) bool { return true }, deliver)
}

// DeliverFilteredHistory applies filtering before counting the result limit.
// It preserves the same authorization and delivery gate as DeliverHistory.
func (s *Store) DeliverFilteredHistory(id string, authenticated nostr.PubKey, filter nostr.Filter, deliver func(nostr.Event) error) error {
	limit := filter.Limit
	if limit == 0 {
		limit = 200
	}
	return s.deliverHistory(id, authenticated, limit, filter.Matches, deliver)
}

func (s *Store) deliverHistory(id string, authenticated nostr.PubKey, limit int, matches func(nostr.Event) bool, deliver func(nostr.Event) error) error {
	if limit < 1 || limit > 200 {
		return errors.New("invalid: history limit")
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.db.View(func(tx *bbolt.Tx) error {
		b, state, err := group(tx, id)
		if err != nil {
			return err
		}
		if !state.Members[authenticated.Hex()] || state.Banned[authenticated.Hex()] {
			return ErrDenied
		}
		cursor := b.Bucket([]byte("messages")).Cursor()
		count := 0
		for _, data := cursor.Last(); data != nil && count < limit; _, data = cursor.Prev() {
			var record Record
			if err := json.Unmarshal(data, &record); err != nil {
				return err
			}
			if !matches(record.Event) {
				continue
			}
			if err := deliver(record.Event); err != nil {
				return err
			}
			count++
		}
		return nil
	})
}

// DeliverLive only releases a stored message to a currently joined identity.
// Supply an event ID, not caller-controlled message contents or group claims.
func (s *Store) DeliverLive(id string, authenticated nostr.PubKey, eventID nostr.ID, deliver func(nostr.Event) error) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.db.View(func(tx *bbolt.Tx) error {
		b, state, err := group(tx, id)
		if err != nil {
			return err
		}
		if !state.Members[authenticated.Hex()] || state.Banned[authenticated.Hex()] {
			return ErrDenied
		}
		seq := b.Bucket([]byte("seen")).Get([]byte(eventID.Hex()))
		if seq == nil {
			return errors.New("invalid: message not stored in this group")
		}
		data := b.Bucket([]byte("messages")).Get(seq)
		if data == nil {
			return errors.New("invalid: not a chat message")
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		return deliver(record.Event)
	})
}
