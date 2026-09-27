package chatstate

import (
	"encoding/json"
	"errors"
	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
	"sort"
)

// DeliverMultiHistory merges bounded per-group histories while holding the
// revocation gate through final delivery. Nonmember groups never enter results.
func (s *Store) DeliverMultiHistory(key nostr.PubKey, f nostr.Filter, deliver func(nostr.Event) error) error {
	ids := f.Tags["h"]
	if len(ids) < 1 || len(ids) > 32 || f.Limit < 0 || f.Limit > 200 {
		return errors.New("invalid: history bounds")
	}
	limit := f.Limit
	if limit == 0 {
		limit = 200
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.db.View(func(tx *bbolt.Tx) error {
		candidates := make([]nostr.Event, 0)
		seen := make(map[string]bool)
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			b, state, err := group(tx, id)
			if err != nil {
				continue
			}
			if !state.Members[key.Hex()] || state.Banned[key.Hex()] {
				continue
			}
			cursor := b.Bucket([]byte("messages")).Cursor()
			count := 0
			for _, data := cursor.Last(); data != nil && count < limit; _, data = cursor.Prev() {
				var record Record
				if err := json.Unmarshal(data, &record); err != nil {
					return err
				}
				if !f.Matches(record.Event) {
					continue
				}
				candidates = append(candidates, record.Event)
				count++
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].CreatedAt != candidates[j].CreatedAt {
				return candidates[i].CreatedAt > candidates[j].CreatedAt
			}
			return candidates[i].ID.Hex() < candidates[j].ID.Hex()
		})
		if len(candidates) > limit {
			candidates = candidates[:limit]
		}
		for _, e := range candidates {
			if err := deliver(e); err != nil {
				return err
			}
		}
		return nil
	})
}
