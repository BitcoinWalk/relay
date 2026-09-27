package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"fiatjaf.com/nostr"
)

const (
	replicaEntitlementVersion      = 1
	replicaEntitlementKind         = nostr.Kind(30305)
	replicaEntitlementPlan         = "bitcoinwalk-paid-city-lifetime-v1"
	replicaEntitlementActive       = "active"
	replicaEntitlementRevoked      = "revoked"
	replicaEntitlementConfirmation = "entitlement-provision-v1"
	replicaEntitlementApplyConfirm = "entitlement-apply-v1"
)

type replicaEntitlementContent struct {
	Version        int    `json:"version"`
	CityID         string `json:"cityId"`
	Plan           string `json:"plan"`
	Status         string `json:"status"`
	EvidenceDigest string `json:"evidenceDigest"`
}

type replicaEntitlementLedgerFile struct {
	Version int           `json:"version"`
	Events  []nostr.Event `json:"events"`
}

type replicaEntitlementRecord struct {
	Event   nostr.Event
	Content replicaEntitlementContent
}

type replicaEntitlementLedger struct {
	current map[string]replicaEntitlementRecord
}

type replicaEntitlementPlanInput struct {
	CurrentRegistryPath   string
	EntitlementLedgerPath string
	CandidateRegistryPath string
	Authority             nostr.PubKey
	CityID                string
	Destination           string
	Confirmation          string
	Now                   time.Time
}

type replicaEntitlementPlanResult struct {
	CityID             string `json:"cityId"`
	Destination        string `json:"destination"`
	EntitlementEventID string `json:"entitlementEventId"`
	PreviousDigest     string `json:"previousRegistryDigest"`
	CandidateDigest    string `json:"candidateRegistryDigest"`
}

type replicaEntitlementApplyInput struct {
	JournalPath           string
	CurrentRegistryPath   string
	EntitlementLedgerPath string
	CandidateRegistryPath string
	Authority             nostr.PubKey
	CityID                string
	Confirmation          string
	Now                   time.Time
}

func exactEntitlementTag(event nostr.Event, name, expected string) bool {
	value, err := uniqueTag(event, name)
	return err == nil && value == expected
}

func decodeEntitlementContent(event nostr.Event, authority nostr.PubKey, now time.Time) (replicaEntitlementContent, error) {
	var content replicaEntitlementContent
	if authority == (nostr.PubKey{}) || authority.Hex() == adminHex || event.Kind != replicaEntitlementKind || event.PubKey != authority || !event.CheckID() || !event.VerifySignature() {
		return content, errors.New("invalid paid-city entitlement authority or signature")
	}
	if event.CreatedAt > nostr.Timestamp(now.Add(5*time.Minute).Unix()) {
		return content, errors.New("paid-city entitlement timestamp is in the future")
	}
	decoder := json.NewDecoder(strings.NewReader(event.Content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&content); err != nil {
		return content, errors.New("invalid paid-city entitlement content")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return content, errors.New("invalid trailing paid-city entitlement content")
	}
	evidence, err := hex.DecodeString(content.EvidenceDigest)
	if err != nil || len(evidence) != 32 || strings.ToLower(content.EvidenceDigest) != content.EvidenceDigest {
		return content, errors.New("invalid paid-city entitlement evidence commitment")
	}
	if content.Version != replicaEntitlementVersion || !uuidPattern.MatchString(content.CityID) || content.Plan != replicaEntitlementPlan || content.Status != replicaEntitlementActive && content.Status != replicaEntitlementRevoked {
		return content, errors.New("invalid paid-city entitlement scope")
	}
	if !exactEntitlementTag(event, "d", content.CityID) || !exactEntitlementTag(event, "i", content.CityID) || !exactEntitlementTag(event, "plan", content.Plan) || !exactEntitlementTag(event, "status", content.Status) {
		return content, errors.New("paid-city entitlement tags do not match its signed content")
	}
	return content, nil
}

func loadReplicaEntitlementLedger(path string, authority nostr.PubKey, now time.Time) (*replicaEntitlementLedger, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || info.Size() <= 0 || info.Size() > 1024*1024 {
		return nil, errors.New("entitlement ledger must be a bounded non-writable regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var input replicaEntitlementLedgerFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid trailing entitlement ledger data")
	}
	if input.Version != replicaEntitlementVersion || len(input.Events) == 0 || len(input.Events) > 1000 {
		return nil, errors.New("invalid entitlement ledger envelope")
	}
	ledger := &replicaEntitlementLedger{current: make(map[string]replicaEntitlementRecord)}
	for _, event := range input.Events {
		content, err := decodeEntitlementContent(event, authority, now)
		if err != nil {
			return nil, err
		}
		current, exists := ledger.current[content.CityID]
		if !exists || event.CreatedAt > current.Event.CreatedAt || event.CreatedAt == current.Event.CreatedAt && event.ID.Hex() > current.Event.ID.Hex() {
			ledger.current[content.CityID] = replicaEntitlementRecord{Event: event, Content: content}
		}
	}
	return ledger, nil
}

func (l *replicaEntitlementLedger) active(cityID string) (replicaEntitlementRecord, bool) {
	if l == nil {
		return replicaEntitlementRecord{}, false
	}
	record, ok := l.current[cityID]
	return record, ok && record.Content.Status == replicaEntitlementActive
}

func validateEntitledReplicaRegistry(registry *replicaRegistry, ledger *replicaEntitlementLedger) error {
	if registry == nil || len(registry.destinations) == 0 || ledger == nil {
		return errors.New("entitlement-required replication needs a registry and ledger")
	}
	for cityID := range registry.destinations {
		entitlementID, ok := registry.entitlement(cityID)
		if !ok {
			return errors.New("replica registry city has no entitlement binding")
		}
		record, active := ledger.active(cityID)
		if !active || record.Event.ID.Hex() != entitlementID {
			return errors.New("replica registry entitlement is absent, revoked or superseded")
		}
	}
	return nil
}

func secureEntitlementCandidatePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("entitlement candidate requires a clean absolute path")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("entitlement candidate must not exist")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&0022 != 0 {
		return errors.New("entitlement candidate directory must be owner-controlled")
	}
	return nil
}

func planEntitledReplicaCity(input replicaEntitlementPlanInput) (result replicaEntitlementPlanResult, err error) {
	if input.Confirmation != replicaEntitlementConfirmation {
		return result, errors.New("paid-city provisioning requires the exact entitlement confirmation")
	}
	if !uuidPattern.MatchString(input.CityID) {
		return result, errors.New("invalid paid-city provisioning city")
	}
	destination, err := normalizeReplicaDestination(input.Destination)
	if err != nil {
		return result, err
	}
	if err := secureEntitlementCandidatePath(input.CandidateRegistryPath); err != nil {
		return result, err
	}
	current, err := loadReplicaRegistry(input.CurrentRegistryPath)
	if err != nil {
		return result, fmt.Errorf("load current replica registry: %w", err)
	}
	if _, exists := current.destination(input.CityID); exists {
		return result, errors.New("paid-city provisioning cannot replace an existing city")
	}
	for _, configured := range current.destinations {
		if configured == destination {
			return result, errors.New("paid-city provisioning destination is already assigned")
		}
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	ledger, err := loadReplicaEntitlementLedger(input.EntitlementLedgerPath, input.Authority, input.Now)
	if err != nil {
		return result, fmt.Errorf("load paid-city entitlement ledger: %w", err)
	}
	record, active := ledger.active(input.CityID)
	if !active {
		return result, errors.New("no current active paid-city entitlement")
	}

	candidate := make([]replicaRegistryCity, 0, len(current.destinations)+1)
	for cityID, configured := range current.destinations {
		entitlementID, _ := current.entitlement(cityID)
		candidate = append(candidate, replicaRegistryCity{CityID: cityID, Destination: configured, EntitlementEventID: entitlementID})
	}
	candidate = append(candidate, replicaRegistryCity{CityID: input.CityID, Destination: destination, EntitlementEventID: record.Event.ID.Hex()})
	slices.SortFunc(candidate, func(a, b replicaRegistryCity) int { return strings.Compare(a.CityID, b.CityID) })
	data, err := json.MarshalIndent(replicaRegistryFile{Version: replicaRegistryVersion, Cities: candidate}, "", "  ")
	if err != nil {
		return result, err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(input.CandidateRegistryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(input.CandidateRegistryPath)
		}
	}()
	if _, err := file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return result, err
	}
	loaded, err := loadReplicaRegistry(input.CandidateRegistryPath)
	if err != nil {
		return result, err
	}
	keep = true
	return replicaEntitlementPlanResult{
		CityID: input.CityID, Destination: destination, EntitlementEventID: record.Event.ID.Hex(),
		PreviousDigest: current.fingerprint(), CandidateDigest: loaded.fingerprint(),
	}, nil
}

func runReplicaEntitlementPlan() error {
	authority, err := nostr.PubKeyFromHex(os.Getenv("RELAY_REPLICA_ENTITLEMENT_PLAN_AUTHORITY"))
	if err != nil {
		return errors.New("invalid paid-city entitlement authority")
	}
	result, err := planEntitledReplicaCity(replicaEntitlementPlanInput{
		CurrentRegistryPath:   os.Getenv("RELAY_REPLICA_ENTITLEMENT_PLAN_CURRENT"),
		EntitlementLedgerPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_PLAN_LEDGER"),
		CandidateRegistryPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_PLAN_CANDIDATE"),
		Authority:             authority,
		CityID:                os.Getenv("RELAY_REPLICA_ENTITLEMENT_PLAN_CITY"),
		Destination:           os.Getenv("RELAY_REPLICA_ENTITLEMENT_PLAN_DESTINATION"),
		Confirmation:          os.Getenv("RELAY_REPLICA_ENTITLEMENT_PLAN_CONFIRM"),
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func applyEntitledReplicaCity(input replicaEntitlementApplyInput) (replicaRegistryAddResult, error) {
	if input.Confirmation != replicaEntitlementApplyConfirm {
		return replicaRegistryAddResult{}, errors.New("paid-city registry activation requires the exact entitlement apply confirmation")
	}
	if !uuidPattern.MatchString(input.CityID) {
		return replicaRegistryAddResult{}, errors.New("invalid paid-city registry activation city")
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	ledger, err := loadReplicaEntitlementLedger(input.EntitlementLedgerPath, input.Authority, input.Now)
	if err != nil {
		return replicaRegistryAddResult{}, fmt.Errorf("load paid-city entitlement ledger: %w", err)
	}
	record, active := ledger.active(input.CityID)
	if !active {
		return replicaRegistryAddResult{}, errors.New("no current active paid-city entitlement")
	}
	candidate, err := loadReplicaRegistry(input.CandidateRegistryPath)
	if err != nil {
		return replicaRegistryAddResult{}, fmt.Errorf("load candidate replica registry: %w", err)
	}
	candidateEntitlementID, bound := candidate.entitlement(input.CityID)
	if !bound || candidateEntitlementID != record.Event.ID.Hex() {
		return replicaRegistryAddResult{}, errors.New("candidate registry does not bind the current paid-city entitlement")
	}
	return addReplicaRegistryCityWithEntitlement(
		input.JournalPath,
		input.CurrentRegistryPath,
		input.CandidateRegistryPath,
		replicaRegistryAddConfirmation,
		record.Event.ID.Hex(),
	)
}

func runReplicaEntitlementApply() error {
	authority, err := nostr.PubKeyFromHex(os.Getenv("RELAY_REPLICA_ENTITLEMENT_APPLY_AUTHORITY"))
	if err != nil {
		return errors.New("invalid paid-city entitlement authority")
	}
	result, err := applyEntitledReplicaCity(replicaEntitlementApplyInput{
		JournalPath:           os.Getenv("RELAY_REPLICA_ENTITLEMENT_APPLY_JOURNAL"),
		CurrentRegistryPath:   os.Getenv("RELAY_REPLICA_ENTITLEMENT_APPLY_CURRENT"),
		EntitlementLedgerPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_APPLY_LEDGER"),
		CandidateRegistryPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_APPLY_CANDIDATE"),
		Authority:             authority,
		CityID:                os.Getenv("RELAY_REPLICA_ENTITLEMENT_APPLY_CITY"),
		Confirmation:          os.Getenv("RELAY_REPLICA_ENTITLEMENT_APPLY_CONFIRM"),
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func replicaEntitlementApplyConfigured() bool {
	for _, name := range []string{
		"RELAY_REPLICA_ENTITLEMENT_APPLY_JOURNAL",
		"RELAY_REPLICA_ENTITLEMENT_APPLY_CURRENT",
		"RELAY_REPLICA_ENTITLEMENT_APPLY_LEDGER",
		"RELAY_REPLICA_ENTITLEMENT_APPLY_CANDIDATE",
		"RELAY_REPLICA_ENTITLEMENT_APPLY_AUTHORITY",
		"RELAY_REPLICA_ENTITLEMENT_APPLY_CITY",
		"RELAY_REPLICA_ENTITLEMENT_APPLY_CONFIRM",
	} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

func replicaEntitlementPlanConfigured() bool {
	for _, name := range []string{
		"RELAY_REPLICA_ENTITLEMENT_PLAN_CURRENT",
		"RELAY_REPLICA_ENTITLEMENT_PLAN_LEDGER",
		"RELAY_REPLICA_ENTITLEMENT_PLAN_CANDIDATE",
		"RELAY_REPLICA_ENTITLEMENT_PLAN_AUTHORITY",
		"RELAY_REPLICA_ENTITLEMENT_PLAN_CITY",
		"RELAY_REPLICA_ENTITLEMENT_PLAN_DESTINATION",
		"RELAY_REPLICA_ENTITLEMENT_PLAN_CONFIRM",
	} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

func replicaEntitlementRuntimeConfigured() bool {
	return os.Getenv("RELAY_REPLICA_REQUIRE_ENTITLEMENTS") != "" ||
		os.Getenv("RELAY_REPLICA_ENTITLEMENT_LEDGER") != "" ||
		os.Getenv("RELAY_REPLICA_ENTITLEMENT_AUTHORITY") != ""
}
