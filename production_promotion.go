package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/boltdb"
)

const productionPromotionConfirmation = "production-promotion-v1"

type productionPromotionCity struct {
	CityID     string `json:"cityId"`
	Slug       string `json:"slug"`
	Tier       string `json:"tier"`
	RevisionID string `json:"revisionId"`
	ApprovalID string `json:"approvalId"`
}

type productionPromotionManifest struct {
	Version int                       `json:"version"`
	Cities  []productionPromotionCity `json:"cities"`
}

type productionPromotionResult struct {
	ManifestDigest string         `json:"manifestDigest"`
	EventDigest    string         `json:"eventDigest"`
	EventCount     int            `json:"eventCount"`
	Kinds          map[string]int `json:"kinds"`
	FreeCities     []string       `json:"freeCities"`
	PaidCities     []string       `json:"paidCities"`
}

func readPromotionManifest(path string) (productionPromotionManifest, string, error) {
	var manifest productionPromotionManifest
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || info.Size() <= 0 || info.Size() > 1024*1024 {
		return manifest, "", errors.New("promotion manifest must be a bounded non-writable regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, "", err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, "", err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest, "", errors.New("invalid trailing promotion manifest data")
	}
	if manifest.Version != 1 || len(manifest.Cities) == 0 || len(manifest.Cities) > 1000 {
		return manifest, "", errors.New("invalid production promotion manifest")
	}
	seenCity, seenSlug := map[string]bool{}, map[string]bool{}
	for _, city := range manifest.Cities {
		if !uuidPattern.MatchString(city.CityID) || !slugPattern.MatchString(city.Slug) || !sizeOK(city.Slug, 2, 63) || (city.Tier != "free" && city.Tier != "paid") || !hexKeyPattern.MatchString(city.RevisionID) || !hexKeyPattern.MatchString(city.ApprovalID) || seenCity[city.CityID] || seenSlug[city.Slug] {
			return manifest, "", errors.New("invalid or duplicate production promotion city")
		}
		seenCity[city.CityID], seenSlug[city.Slug] = true, true
	}
	digest := sha256.Sum256(data)
	return manifest, hex.EncodeToString(digest[:]), nil
}

func promotionDestinationOK(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("promotion destination must be a clean absolute path")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("promotion destination must not exist")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&0022 != 0 {
		return errors.New("promotion destination directory must be owner-controlled")
	}
	return nil
}

func promotionEventCity(event nostr.Event, selected map[string]bool) bool {
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		if tag[0] == "i" && selected[tag[1]] {
			return true
		}
		if tag[0] == "d" {
			for cityID := range selected {
				if workflowAddress(tag[1], cityID) {
					return true
				}
			}
		}
		if tag[0] == "scope" {
			for cityID := range selected {
				if tag[1] == "city:"+cityID || strings.HasPrefix(tag[1], "walk:"+cityID+":") {
					return true
				}
			}
		}
	}
	var content any
	if json.Unmarshal([]byte(event.Content), &content) == nil && jsonContainsSelectedCity(content, selected) {
		return true
	}
	return false
}

func jsonContainsSelectedCity(value any, selected map[string]bool) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "cityId" {
				if cityID, ok := child.(string); ok && selected[cityID] {
					return true
				}
			}
			if jsonContainsSelectedCity(child, selected) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if jsonContainsSelectedCity(child, selected) {
				return true
			}
		}
	}
	return false
}

func globalPromotionEvent(event nostr.Event, admin nostr.PubKey) bool {
	if event.PubKey != admin {
		return false
	}
	if event.Kind == contentPageKind || event.Kind == featureFlagsKind {
		return true
	}
	if event.Kind != eventModerationKind {
		return false
	}
	moderation, err := parseEventModeration(event)
	return err == nil && moderation.Scope == "organizer"
}

func currentPromotionApproval(db *boltdb.BoltBackend, cityID string, admin nostr.PubKey) (*nostr.Event, cityDecision, error) {
	var records []nostr.Event
	for event := range db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30304}, Authors: []nostr.PubKey{admin}}, 10001) {
		var decision cityDecision
		status, statusErr := uniqueTag(event, "status")
		address, addressErr := uniqueTag(event, "d")
		if json.Unmarshal([]byte(event.Content), &decision) == nil && decision.CityID == cityID && statusErr == nil && status == decision.Status && addressErr == nil && workflowAddress(address, cityID) && event.CheckID() && event.VerifySignature() {
			records = append(records, event)
		}
	}
	if len(records) > 10000 {
		return nil, cityDecision{}, errors.New("promotion approval history exceeds limit")
	}
	slices.SortFunc(records, func(left, right nostr.Event) int {
		if left.CreatedAt != right.CreatedAt {
			if left.CreatedAt > right.CreatedAt {
				return -1
			}
			return 1
		}
		return strings.Compare(right.ID.Hex(), left.ID.Hex())
	})
	rejected := map[string]bool{}
	for _, event := range records {
		var decision cityDecision
		_ = json.Unmarshal([]byte(event.Content), &decision)
		if decision.Status == "rejected" {
			rejected[decision.RevisionID] = true
			continue
		}
		if rejected[decision.RevisionID] {
			decision.Status = "rejected"
		}
		copy := event
		return &copy, decision, nil
	}
	return nil, cityDecision{}, errors.New("promotion city has no retained decision")
}

func buildProductionPromotion(sourcePath, destinationPath, manifestPath, confirmation string) (productionPromotionResult, error) {
	return buildProductionPromotionWithAdmin(sourcePath, destinationPath, manifestPath, confirmation, nostr.MustPubKeyFromHex(adminHex))
}

func buildProductionPromotionWithAdmin(sourcePath, destinationPath, manifestPath, confirmation string, admin nostr.PubKey) (result productionPromotionResult, err error) {
	if confirmation != productionPromotionConfirmation {
		return result, errors.New("production promotion requires exact confirmation")
	}
	if err := promotionDestinationOK(destinationPath); err != nil {
		return result, err
	}
	manifest, manifestDigest, err := readPromotionManifest(manifestPath)
	if err != nil {
		return result, err
	}
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Mode()&os.ModeSymlink != 0 || sourceInfo.Size() <= 0 {
		return result, errors.New("promotion source must be a regular database snapshot")
	}
	source := &boltdb.BoltBackend{Path: sourcePath}
	if err := source.Init(); err != nil {
		return result, err
	}
	defer source.Close()
	selected := make(map[string]bool, len(manifest.Cities))
	for _, city := range manifest.Cities {
		selected[city.CityID] = true
		approval, decision, err := currentPromotionApproval(source, city.CityID, admin)
		if err != nil || approval == nil || decision.Status != "approved" || approval.ID.Hex() != city.ApprovalID || decision.RevisionID != city.RevisionID {
			return result, fmt.Errorf("promotion manifest is not the current approved state for %s", city.CityID)
		}
		revision := func() *nostr.Event {
			id, parseErr := nostr.IDFromHex(city.RevisionID)
			if parseErr != nil {
				return nil
			}
			for event := range source.QueryEvents(nostr.Filter{IDs: []nostr.ID{id}}, 1) {
				copy := event
				return &copy
			}
			return nil
		}()
		if revision == nil {
			return result, fmt.Errorf("promotion revision unavailable for %s", city.CityID)
		}
		draft, err := parseDraft(*revision)
		if err != nil || draft.CityID != city.CityID || func() string {
			if decision.Slug != "" {
				return decision.Slug
			}
			return draft.Slug
		}() != city.Slug {
			return result, fmt.Errorf("promotion revision does not match %s", city.CityID)
		}
	}

	var retained []nostr.Event
	for event := range source.QueryEvents(nostr.Filter{}, 50001) {
		if len(retained) >= 50000 {
			return result, errors.New("promotion source exceeds event limit")
		}
		if !event.CheckID() || !event.VerifySignature() {
			return result, errors.New("promotion source contains a corrupt signed event")
		}
		if globalPromotionEvent(event, admin) || promotionEventCity(event, selected) {
			retained = append(retained, event)
		}
	}
	if len(retained) == 0 {
		return result, errors.New("promotion selected no events")
	}
	slices.SortFunc(retained, func(left, right nostr.Event) int {
		if left.CreatedAt != right.CreatedAt {
			if left.CreatedAt < right.CreatedAt {
				return -1
			}
			return 1
		}
		return strings.Compare(left.ID.Hex(), right.ID.Hex())
	})
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
	hash := sha256.New()
	result = productionPromotionResult{ManifestDigest: manifestDigest, EventCount: len(retained), Kinds: map[string]int{}}
	for _, event := range retained {
		if err := destination.SaveEvent(event); err != nil && !errors.Is(err, eventstore.ErrDupEvent) {
			return result, err
		}
		_, _ = io.WriteString(hash, event.ID.Hex()+"\n")
		result.Kinds[fmt.Sprint(event.Kind)]++
	}
	for _, city := range manifest.Cities {
		approval, decision, err := currentPromotionApproval(destination, city.CityID, admin)
		if err != nil || approval == nil || approval.ID.Hex() != city.ApprovalID || decision.Status != "approved" || decision.RevisionID != city.RevisionID {
			return result, fmt.Errorf("promoted state failed read-back for %s", city.CityID)
		}
		if city.Tier == "paid" {
			result.PaidCities = append(result.PaidCities, city.CityID)
		} else {
			result.FreeCities = append(result.FreeCities, city.CityID)
		}
	}
	slices.Sort(result.FreeCities)
	slices.Sort(result.PaidCities)
	result.EventDigest = hex.EncodeToString(hash.Sum(nil))
	keep = true
	return result, nil
}

func productionPromotionConfigured() bool {
	return os.Getenv("RELAY_PRODUCTION_PROMOTION_SOURCE") != "" || os.Getenv("RELAY_PRODUCTION_PROMOTION_DESTINATION") != "" || os.Getenv("RELAY_PRODUCTION_PROMOTION_MANIFEST") != "" || os.Getenv("RELAY_PRODUCTION_PROMOTION_CONFIRM") != ""
}

func runProductionPromotion() error {
	result, err := buildProductionPromotion(
		os.Getenv("RELAY_PRODUCTION_PROMOTION_SOURCE"),
		os.Getenv("RELAY_PRODUCTION_PROMOTION_DESTINATION"),
		os.Getenv("RELAY_PRODUCTION_PROMOTION_MANIFEST"),
		os.Getenv("RELAY_PRODUCTION_PROMOTION_CONFIRM"),
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
