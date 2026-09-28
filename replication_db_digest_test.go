package main

import (
	"os"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"
)

func TestLogicalBoltDigestTracksRecordsNotFileMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "digest.db")
	write := func(value string) {
		db, err := bbolt.Open(path, 0600, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = db.Update(func(tx *bbolt.Tx) error {
			bucket, err := tx.CreateBucketIfNotExists([]byte("outer"))
			if err != nil {
				return err
			}
			child, err := bucket.CreateBucketIfNotExists([]byte("inner"))
			if err != nil {
				return err
			}
			return child.Put([]byte("key"), []byte(value))
		})
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	write("one")
	first, err := logicalBoltDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := logicalBoltDigest(path)
	if err != nil || first != second {
		t.Fatalf("logical digest changed after a metadata-only reopen: %s %s %v", first, second, err)
	}
	write("two")
	third, err := logicalBoltDigest(path)
	if err != nil || third == second {
		t.Fatalf("logical digest missed a record change: %s %s %v", second, third, err)
	}
	db, err = bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte("outer")).Bucket([]byte("inner")).SetSequence(42)
	})
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	fourth, err := logicalBoltDigest(path)
	if err != nil || fourth == third {
		t.Fatalf("logical digest missed a bucket sequence change: %s %s %v", third, fourth, err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := logicalBoltDigest(path); err == nil {
		t.Fatal("logical digest accepted a group/world-writable database")
	}
}

func TestReplicaJournalStableDigestExcludesOnlyReconciliationTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	write := func(reconciled, event string) {
		db, err := bbolt.Open(path, 0600, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = db.Update(func(tx *bbolt.Tx) error {
			metadata, err := tx.CreateBucketIfNotExists(replicaMetaBucket)
			if err != nil {
				return err
			}
			if err := metadata.Put(replicaReconciledKey, []byte(reconciled)); err != nil {
				return err
			}
			outbox, err := tx.CreateBucketIfNotExists(replicaOutboxBucket)
			if err != nil {
				return err
			}
			return outbox.Put([]byte("event"), []byte(event))
		})
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	write("100", "one")
	stableOne, err := logicalReplicaJournalStableDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	fullOne, err := logicalBoltDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	write("200", "one")
	stableTwo, err := logicalReplicaJournalStableDigest(path)
	if err != nil || stableOne != stableTwo {
		t.Fatalf("stable digest changed for reconciliation timestamp: %s %s %v", stableOne, stableTwo, err)
	}
	fullTwo, err := logicalBoltDigest(path)
	if err != nil || fullOne == fullTwo {
		t.Fatalf("full digest ignored reconciliation timestamp: %s %s %v", fullOne, fullTwo, err)
	}
	write("300", "two")
	stableThree, err := logicalReplicaJournalStableDigest(path)
	if err != nil || stableTwo == stableThree {
		t.Fatalf("stable digest ignored operational record change: %s %s %v", stableTwo, stableThree, err)
	}
}
