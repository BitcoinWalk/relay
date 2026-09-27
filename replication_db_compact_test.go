package main

import (
	"os"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"
)

func makeFragmentedBoltDB(t *testing.T, path string) {
	t.Helper()
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucket([]byte("events"))
		if err != nil {
			return err
		}
		for i := 0; i < 2000; i++ {
			key := []byte{byte(i >> 8), byte(i)}
			value := make([]byte, 2048)
			value[0] = byte(i)
			if err := bucket.Put(key, value); err != nil {
				return err
			}
		}
		return bucket.SetSequence(918)
	})
	if err == nil {
		err = db.Update(func(tx *bbolt.Tx) error {
			bucket := tx.Bucket([]byte("events"))
			for i := 0; i < 1900; i++ {
				if err := bucket.Delete([]byte{byte(i >> 8), byte(i)}); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompactBoltDBPreservesLogicalStateAndReclaimsSpace(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.db")
	destination := filepath.Join(dir, "compacted.db")
	makeFragmentedBoltDB(t, source)

	result, err := compactBoltDB(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Equal || result.SourceDigest == "" || result.SourceDigest != result.DestinationDigest {
		t.Fatalf("unexpected compaction result: %+v", result)
	}
	if result.DestinationBytes >= result.SourceBytes {
		t.Fatalf("compaction did not reclaim space: %+v", result)
	}
	if info, err := os.Stat(destination); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("destination mode is not owner-only: %v %v", info, err)
	}

	db, err := bbolt.Open(destination, 0600, &bbolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("events"))
		retained := 1999
		if bucket == nil || bucket.Get([]byte{byte(retained >> 8), byte(retained)}) == nil {
			t.Fatal("retained record is missing after compaction")
		}
		if bucket.Get([]byte{0, 1}) != nil {
			t.Fatal("deleted record was resurrected after compaction")
		}
		if bucket.Sequence() != 918 {
			t.Fatalf("bucket sequence changed after compaction: %d", bucket.Sequence())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompactBoltDBRejectsUnsafePathsWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.db")
	makeFragmentedBoltDB(t, source)

	t.Run("same path", func(t *testing.T) {
		if _, err := compactBoltDB(source, source); err == nil {
			t.Fatal("accepted identical source and destination")
		}
	})
	t.Run("relative path", func(t *testing.T) {
		if _, err := compactBoltDB("source.db", filepath.Join(dir, "relative.db")); err == nil {
			t.Fatal("accepted a relative source path")
		}
	})
	t.Run("source symlink", func(t *testing.T) {
		link := filepath.Join(dir, "source-link.db")
		if err := os.Symlink(source, link); err != nil {
			t.Fatal(err)
		}
		if _, err := compactBoltDB(link, filepath.Join(dir, "link-output.db")); err == nil {
			t.Fatal("accepted a symlink source")
		}
	})
	t.Run("writable source", func(t *testing.T) {
		if err := os.Chmod(source, 0666); err != nil {
			t.Fatal(err)
		}
		if _, err := compactBoltDB(source, filepath.Join(dir, "writable-output.db")); err == nil {
			t.Fatal("accepted a group/world-writable source")
		}
		if err := os.Chmod(source, 0600); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("existing destination", func(t *testing.T) {
		destination := filepath.Join(dir, "existing.db")
		const sentinel = "do not replace"
		if err := os.WriteFile(destination, []byte(sentinel), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := compactBoltDB(source, destination); err == nil {
			t.Fatal("accepted an existing destination")
		}
		data, err := os.ReadFile(destination)
		if err != nil || string(data) != sentinel {
			t.Fatalf("existing destination was changed: %q %v", data, err)
		}
	})
}
