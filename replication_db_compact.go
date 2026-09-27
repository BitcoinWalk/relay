package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
)

type boltCompactionResult struct {
	SourceDigest      string `json:"sourceDigest"`
	DestinationDigest string `json:"destinationDigest"`
	SourceBytes       int64  `json:"sourceBytes"`
	DestinationBytes  int64  `json:"destinationBytes"`
	Equal             bool   `json:"equal"`
}

func secureCompactPath(path string, source bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("bolt compaction requires clean absolute paths")
	}
	if source {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("bolt compaction source must be a non-writable regular file")
		}
		return nil
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("bolt compaction destination must not exist")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&0022 != 0 {
		return errors.New("bolt compaction destination directory must be an owner-controlled directory")
	}
	return nil
}

func compactBoltDB(source, destination string) (result boltCompactionResult, err error) {
	if source == destination {
		return result, errors.New("bolt compaction source and destination must differ")
	}
	if err := secureCompactPath(source, true); err != nil {
		return result, err
	}
	if err := secureCompactPath(destination, false); err != nil {
		return result, err
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return result, err
	}
	result.SourceBytes = sourceInfo.Size()
	result.SourceDigest, err = logicalBoltDigest(source)
	if err != nil {
		return result, err
	}

	reserved, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	if err := reserved.Close(); err != nil {
		_ = os.Remove(destination)
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(destination)
		}
	}()

	sourceDB, err := bbolt.Open(source, 0600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return result, err
	}
	destinationDB, err := bbolt.Open(destination, 0600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		_ = sourceDB.Close()
		return result, err
	}
	compactErr := bbolt.Compact(destinationDB, sourceDB, 64*1024*1024)
	closeDestinationErr := destinationDB.Close()
	closeSourceErr := sourceDB.Close()
	if compactErr != nil {
		return result, compactErr
	}
	if closeDestinationErr != nil {
		return result, closeDestinationErr
	}
	if closeSourceErr != nil {
		return result, closeSourceErr
	}
	if err := os.Chmod(destination, 0600); err != nil {
		return result, err
	}
	result.DestinationDigest, err = logicalBoltDigest(destination)
	if err != nil {
		return result, err
	}
	result.Equal = result.SourceDigest == result.DestinationDigest
	if !result.Equal {
		return result, errors.New("compacted database logical digest mismatch")
	}
	destinationInfo, err := os.Stat(destination)
	if err != nil {
		return result, err
	}
	result.DestinationBytes = destinationInfo.Size()
	keep = true
	return result, nil
}

func runReplicaDBCompact(source, destination string) error {
	if source == "" || destination == "" {
		return errors.New("RELAY_REPLICA_DB_COMPACT_SOURCE and RELAY_REPLICA_DB_COMPACT_DESTINATION must be configured together")
	}
	result, err := compactBoltDB(source, destination)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
