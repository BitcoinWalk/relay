package chatstate

import (
	"encoding/json"
	"go.etcd.io/bbolt"
	"testing"
)

func TestRecoveryRejectsAuthorityTampering(t *testing.T) {
	s, _, _ := openTest(t)
	if err := s.VerifyRecovery(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Update(func(tx *bbolt.Tx) error {
		b, state, err := group(tx, "city-a")
		if err != nil {
			return err
		}
		state.Banned["unaudited"] = true
		data, _ := json.Marshal(state)
		return b.Put([]byte("state"), data)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyRecovery(); err == nil {
		t.Fatal("unaudited authority change accepted")
	}
}
