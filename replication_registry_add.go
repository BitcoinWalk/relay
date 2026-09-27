package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"go.etcd.io/bbolt"
)

const replicaRegistryAddConfirmation = "staging-registry-add-v1"

type replicaRegistryAddResult struct {
	CityID         string `json:"addedCityId"`
	Destination    string `json:"addedDestination"`
	PreviousDigest string `json:"previousRegistryDigest"`
	CurrentDigest  string `json:"currentRegistryDigest"`
}

func validateRetainedReplicaRows(tx *bbolt.Tx, current *replicaRegistry, addedCityID string) error {
	entries := tx.Bucket(replicaEntriesBucket)
	outbox := tx.Bucket(replicaOutboxBucket)
	heads := tx.Bucket(replicaHeadsBucket)
	if entries == nil || outbox == nil || heads == nil {
		return errors.New("replica registry add requires an initialized journal")
	}
	if heads.Get([]byte(addedCityID)) != nil {
		return errors.New("replica registry add found retained state for the new city")
	}
	if err := entries.ForEach(func(_, data []byte) error {
		var entry replicaJournalEntry
		if json.Unmarshal(data, &entry) != nil {
			return errors.New("replica registry add found an invalid journal entry")
		}
		destination, ok := current.destination(entry.CityID)
		if !ok || destination != entry.Destination || entry.CityID == addedCityID {
			return errors.New("replica registry add found journal state outside the current registry")
		}
		return nil
	}); err != nil {
		return err
	}
	return outbox.ForEach(func(_, data []byte) error {
		var row replicaOutboxRow
		if json.Unmarshal(data, &row) != nil {
			return errors.New("replica registry add found an invalid outbox row")
		}
		destination, ok := current.destination(row.CityID)
		if !ok || destination != row.Destination || row.CityID == addedCityID {
			return errors.New("replica registry add found outbox state outside the current registry")
		}
		return nil
	})
}

func addReplicaRegistryCity(journalPath, currentPath, candidatePath, confirmation string) (replicaRegistryAddResult, error) {
	return addReplicaRegistryCityWithEntitlement(journalPath, currentPath, candidatePath, confirmation, "")
}

func addReplicaRegistryCityWithEntitlement(journalPath, currentPath, candidatePath, confirmation, expectedEntitlementID string) (replicaRegistryAddResult, error) {
	if confirmation != replicaRegistryAddConfirmation {
		return replicaRegistryAddResult{}, errors.New("replica registry add requires the exact staging confirmation")
	}
	current, err := loadReplicaRegistry(currentPath)
	if err != nil {
		return replicaRegistryAddResult{}, fmt.Errorf("load current replica registry: %w", err)
	}
	candidate, err := loadReplicaRegistry(candidatePath)
	if err != nil {
		return replicaRegistryAddResult{}, fmt.Errorf("load candidate replica registry: %w", err)
	}
	if len(candidate.destinations) != len(current.destinations)+1 {
		return replicaRegistryAddResult{}, errors.New("replica registry add requires exactly one new city")
	}
	for cityID, destination := range current.destinations {
		if retained, ok := candidate.destination(cityID); !ok || retained != destination {
			return replicaRegistryAddResult{}, errors.New("replica registry add cannot remove or redirect an existing city")
		}
		currentEntitlement, currentBound := current.entitlement(cityID)
		candidateEntitlement, candidateBound := candidate.entitlement(cityID)
		if currentBound != candidateBound || currentEntitlement != candidateEntitlement {
			return replicaRegistryAddResult{}, errors.New("replica registry add cannot remove or replace an existing entitlement binding")
		}
	}
	addedCityID, addedDestination := "", ""
	for cityID, destination := range candidate.destinations {
		if _, exists := current.destination(cityID); !exists {
			addedCityID, addedDestination = cityID, destination
		}
	}
	if addedCityID == "" {
		return replicaRegistryAddResult{}, errors.New("replica registry add did not find a new city")
	}
	addedEntitlementID, entitlementBound := candidate.entitlement(addedCityID)
	if expectedEntitlementID == "" && entitlementBound {
		return replicaRegistryAddResult{}, errors.New("entitlement-bound registry add requires the entitlement apply workflow")
	}
	if expectedEntitlementID != "" && (!entitlementBound || addedEntitlementID != expectedEntitlementID) {
		return replicaRegistryAddResult{}, errors.New("candidate registry does not match the current paid-city entitlement")
	}
	info, err := os.Lstat(journalPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return replicaRegistryAddResult{}, errors.New("replica registry add journal must be a non-writable regular file")
	}
	db, err := bbolt.Open(journalPath, 0600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return replicaRegistryAddResult{}, fmt.Errorf("open replica registry add journal: %w", err)
	}
	defer db.Close()
	previousDigest, currentDigest := current.fingerprint(), candidate.fingerprint()
	err = db.Update(func(tx *bbolt.Tx) error {
		meta := tx.Bucket(replicaMetaBucket)
		if meta == nil || !bytes.Equal(meta.Get(replicaRegistryKey), []byte(previousDigest)) {
			return errors.New("replica registry add current digest does not match the journal")
		}
		if err := validateRetainedReplicaRows(tx, current, addedCityID); err != nil {
			return err
		}
		return meta.Put(replicaRegistryKey, []byte(currentDigest))
	})
	if err != nil {
		return replicaRegistryAddResult{}, err
	}
	return replicaRegistryAddResult{CityID: addedCityID, Destination: addedDestination, PreviousDigest: previousDigest, CurrentDigest: currentDigest}, nil
}

func runReplicaRegistryAdd() error {
	result, err := addReplicaRegistryCity(
		os.Getenv("RELAY_REPLICA_JOURNAL"),
		os.Getenv("RELAY_REPLICA_REGISTRY_ADD_CURRENT"),
		os.Getenv("RELAY_REPLICA_REGISTRY_ADD_CANDIDATE"),
		os.Getenv("RELAY_REPLICA_REGISTRY_ADD_CONFIRM"),
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
