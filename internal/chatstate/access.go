package chatstate

import (
	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

// CheckMember is only a preliminary check. Use the delivery methods to avoid
// a check/use race at the actual transport write.
func (s *Store) CheckMember(id string, key nostr.PubKey) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.db.View(func(tx *bbolt.Tx) error {
		_, state, err := group(tx, id)
		if err != nil {
			return err
		}
		if !state.Members[key.Hex()] || state.Banned[key.Hex()] {
			return ErrDenied
		}
		return nil
	})
}
