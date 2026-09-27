package chatstate

import (
	"go.etcd.io/bbolt"
	"os"
)

// Backup writes a consistent database snapshot, never overwriting an existing
// file. The relay signing key must be backed up separately by the operator.
// The caller owns retention and removal of any partial file on error.
func (s *Store) Backup(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	s.gate.RLock()
	err = s.db.View(func(tx *bbolt.Tx) error { _, err := tx.WriteTo(f); return err })
	s.gate.RUnlock()
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
