package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/boltdb"
)

const productionAdditiveConfirmation = "production-additive-v1"

type productionAdditiveResult struct {
	ManifestDigest   string         `json:"manifestDigest"`
	BaseEventCount   int            `json:"baseEventCount"`
	AddedEventCount  int            `json:"addedEventCount"`
	FinalEventCount  int            `json:"finalEventCount"`
	FinalEventDigest string         `json:"finalEventDigest"`
	AddedEventDigest string         `json:"addedEventDigest"`
	AddedKinds       map[string]int `json:"addedKinds"`
	AddedEventIDs    []string       `json:"addedEventIds"`
	Cities           []string       `json:"cities"`
}

func buildProductionAdditive(sourcePath, basePath, destinationPath, manifestPath, confirmation string) (productionAdditiveResult, error) {
	return buildProductionAdditiveWithAdmin(sourcePath, basePath, destinationPath, manifestPath, confirmation, nostr.MustPubKeyFromHex(adminHex))
}

func buildProductionAdditiveWithAdmin(sourcePath, basePath, destinationPath, manifestPath, confirmation string, admin nostr.PubKey) (result productionAdditiveResult, err error) {
	if confirmation != productionAdditiveConfirmation {
		return result, errors.New("production additive promotion requires exact confirmation")
	}
	if err := promotionDestinationOK(destinationPath); err != nil {
		return result, err
	}
	baseInfo, err := os.Lstat(basePath)
	if err != nil || !baseInfo.Mode().IsRegular() || baseInfo.Mode()&os.ModeSymlink != 0 || baseInfo.Size() <= 0 {
		return result, errors.New("production additive base must be a regular database snapshot")
	}
	manifest, manifestDigest, err := readPromotionManifest(manifestPath)
	if err != nil {
		return result, err
	}
	selectedPath := destinationPath + ".selected"
	if _, err := os.Lstat(selectedPath); !os.IsNotExist(err) {
		return result, errors.New("production additive selected database must not exist")
	}
	defer os.Remove(selectedPath)
	if _, err := buildProductionPromotionWithAdmin(sourcePath, selectedPath, manifestPath, productionPromotionConfirmation, admin); err != nil {
		return result, err
	}

	selectedCities := make(map[string]bool, len(manifest.Cities))
	for _, city := range manifest.Cities {
		selectedCities[city.CityID] = true
		result.Cities = append(result.Cities, city.CityID)
	}
	slices.Sort(result.Cities)

	base := &boltdb.BoltBackend{Path: basePath}
	if err := base.Init(); err != nil {
		return result, err
	}
	defer base.Close()
	selected := &boltdb.BoltBackend{Path: selectedPath}
	if err := selected.Init(); err != nil {
		return result, err
	}
	defer selected.Close()
	destination := &boltdb.BoltBackend{Path: destinationPath}
	if err := destination.Init(); err != nil {
		return result, err
	}
	keep := false
	defer func() {
		destination.Close()
		if !keep {
			_ = os.Remove(destinationPath)
		}
	}()

	known := map[string]bool{}
	var finalIDs []string
	for event := range base.QueryEvents(nostr.Filter{}, 50001) {
		if len(known) >= 50000 || !event.CheckID() || !event.VerifySignature() {
			return result, errors.New("production additive base is corrupt or exceeds event limit")
		}
		id := event.ID.Hex()
		known[id] = true
		finalIDs = append(finalIDs, id)
		if err := destination.SaveEvent(event); err != nil && !errors.Is(err, eventstore.ErrDupEvent) {
			return result, err
		}
	}
	result.BaseEventCount = len(known)

	var additions []nostr.Event
	for event := range selected.QueryEvents(nostr.Filter{}, 50001) {
		if !promotionEventCity(event, selectedCities) || known[event.ID.Hex()] {
			continue
		}
		if !event.CheckID() || !event.VerifySignature() {
			return result, errors.New("production additive selection contains a corrupt signed event")
		}
		additions = append(additions, event)
	}
	slices.SortFunc(additions, func(left, right nostr.Event) int {
		if left.CreatedAt != right.CreatedAt {
			if left.CreatedAt < right.CreatedAt {
				return -1
			}
			return 1
		}
		return slices.Compare([]byte(left.ID.Hex()), []byte(right.ID.Hex()))
	})
	if len(additions) == 0 {
		return result, errors.New("production additive promotion selected no new events")
	}
	addedHash := sha256.New()
	result.AddedKinds = map[string]int{}
	for _, event := range additions {
		if err := destination.SaveEvent(event); err != nil && !errors.Is(err, eventstore.ErrDupEvent) {
			return result, err
		}
		id := event.ID.Hex()
		known[id] = true
		finalIDs = append(finalIDs, id)
		result.AddedEventIDs = append(result.AddedEventIDs, id)
		result.AddedKinds[fmt.Sprint(event.Kind)]++
		_, _ = io.WriteString(addedHash, id+"\n")
	}

	for _, city := range manifest.Cities {
		approval, decision, err := currentPromotionApproval(destination, city.CityID, admin)
		if err != nil || approval == nil || approval.ID.Hex() != city.ApprovalID || decision.Status != "approved" || decision.RevisionID != city.RevisionID {
			return result, fmt.Errorf("additive production state failed read-back for %s", city.CityID)
		}
	}
	slices.Sort(finalIDs)
	finalHash := sha256.New()
	for _, id := range finalIDs {
		_, _ = io.WriteString(finalHash, id+"\n")
	}
	result.ManifestDigest = manifestDigest
	result.AddedEventCount = len(additions)
	result.FinalEventCount = len(known)
	result.AddedEventDigest = hex.EncodeToString(addedHash.Sum(nil))
	result.FinalEventDigest = hex.EncodeToString(finalHash.Sum(nil))
	keep = true
	return result, nil
}

func productionAdditiveConfigured() bool {
	return os.Getenv("RELAY_PRODUCTION_ADDITIVE_SOURCE") != "" || os.Getenv("RELAY_PRODUCTION_ADDITIVE_BASE") != "" || os.Getenv("RELAY_PRODUCTION_ADDITIVE_DESTINATION") != "" || os.Getenv("RELAY_PRODUCTION_ADDITIVE_MANIFEST") != "" || os.Getenv("RELAY_PRODUCTION_ADDITIVE_CONFIRM") != ""
}

func runProductionAdditive() error {
	result, err := buildProductionAdditive(
		os.Getenv("RELAY_PRODUCTION_ADDITIVE_SOURCE"),
		os.Getenv("RELAY_PRODUCTION_ADDITIVE_BASE"),
		os.Getenv("RELAY_PRODUCTION_ADDITIVE_DESTINATION"),
		os.Getenv("RELAY_PRODUCTION_ADDITIVE_MANIFEST"),
		os.Getenv("RELAY_PRODUCTION_ADDITIVE_CONFIRM"),
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
