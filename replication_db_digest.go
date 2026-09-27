package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"os"
	"time"

	"go.etcd.io/bbolt"
)

func writeDigestPart(digest hash.Hash, marker byte, values ...[]byte) error {
	if _, err := digest.Write([]byte{marker}); err != nil {
		return err
	}
	for _, value := range values {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		if _, err := digest.Write(length[:]); err != nil {
			return err
		}
		if _, err := digest.Write(value); err != nil {
			return err
		}
	}
	return nil
}

func boltSequenceBytes(sequence uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, sequence)
	return encoded
}

func digestBoltBucket(digest hash.Hash, path []byte, bucket *bbolt.Bucket) error {
	return bucket.ForEach(func(key, value []byte) error {
		if value != nil {
			return writeDigestPart(digest, 'k', path, key, value)
		}
		child := bucket.Bucket(key)
		if child == nil {
			return errors.New("bolt digest encountered an invalid nested bucket")
		}
		childPath := make([]byte, 0, len(path)+1+len(key))
		childPath = append(childPath, path...)
		childPath = append(childPath, 0)
		childPath = append(childPath, key...)
		if err := writeDigestPart(digest, 'b', childPath, boltSequenceBytes(child.Sequence())); err != nil {
			return err
		}
		return digestBoltBucket(digest, childPath, child)
	})
}

func logicalBoltDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return "", errors.New("bolt digest requires a non-writable regular file")
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return "", err
	}
	defer db.Close()
	digest := sha256.New()
	err = db.View(func(tx *bbolt.Tx) error {
		return tx.ForEach(func(name []byte, bucket *bbolt.Bucket) error {
			if err := writeDigestPart(digest, 'B', name, boltSequenceBytes(bucket.Sequence())); err != nil {
				return err
			}
			return digestBoltBucket(digest, name, bucket)
		})
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func runReplicaDBDigest(path string) error {
	digest, err := logicalBoltDigest(path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.WriteString(digest + "\n")
	return err
}
