package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"fiatjaf.com/nostr"
)

const (
	replicaEntitlementProductionIssueConfirm = "entitlement-production-issue-v1"
	replicaEntitlementProductionBasis        = "production-payment-v1"
	replicaEntitlementBootstrapConfirm       = "entitlement-production-bootstrap-v1"
)

type replicaProductionEntitlementIssueInput struct {
	AuthorityKeyPath string
	SourceLedgerPath string
	LedgerPath       string
	CityID           string
	Status           string
	EvidenceDigest   string
	Confirmation     string
	Now              time.Time
}

type replicaProductionEntitlementIssueResult struct {
	CityID             string `json:"cityId"`
	Authority          string `json:"authority"`
	EntitlementEventID string `json:"entitlementEventId"`
	Status             string `json:"status"`
	EvidenceDigest     string `json:"evidenceDigest"`
}

func issueProductionReplicaEntitlement(input replicaProductionEntitlementIssueInput) (result replicaProductionEntitlementIssueResult, err error) {
	if input.Confirmation != replicaEntitlementProductionIssueConfirm {
		return result, errors.New("production entitlement issue requires the exact operator confirmation")
	}
	if !uuidPattern.MatchString(input.CityID) || input.Status != replicaEntitlementActive && input.Status != replicaEntitlementRevoked {
		return result, errors.New("invalid production entitlement scope")
	}
	evidence, err := hex.DecodeString(input.EvidenceDigest)
	if err != nil || len(evidence) != 32 || hex.EncodeToString(evidence) != input.EvidenceDigest {
		return result, errors.New("production entitlement requires a lowercase SHA-256 payment evidence commitment")
	}
	if !filepath.IsAbs(input.AuthorityKeyPath) || filepath.Clean(input.AuthorityKeyPath) != input.AuthorityKeyPath {
		return result, errors.New("production entitlement authority requires a clean absolute path")
	}
	if err := secureEntitlementCandidatePath(input.LedgerPath); err != nil {
		return result, err
	}
	key, err := loadReplicaServiceKey(input.AuthorityKeyPath)
	if err != nil {
		return result, err
	}
	authority := nostr.GetPublicKey(key)
	if authority.Hex() == adminHex {
		return result, errors.New("relay super-admin cannot issue production entitlements")
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	events := make([]nostr.Event, 0, 1)
	if input.SourceLedgerPath != "" {
		if !filepath.IsAbs(input.SourceLedgerPath) || filepath.Clean(input.SourceLedgerPath) != input.SourceLedgerPath || input.SourceLedgerPath == input.LedgerPath {
			return result, errors.New("production entitlement source requires a distinct clean absolute path")
		}
		if _, err := loadReplicaEntitlementLedger(input.SourceLedgerPath, authority, input.Now); err != nil {
			return result, err
		}
		data, err := os.ReadFile(input.SourceLedgerPath)
		if err != nil {
			return result, err
		}
		var source replicaEntitlementLedgerFile
		if json.Unmarshal(data, &source) != nil || len(source.Events) >= 1000 {
			return result, errors.New("invalid production entitlement source ledger")
		}
		events = append(events, source.Events...)
	}
	content, err := json.Marshal(replicaEntitlementContent{Version: replicaEntitlementVersion, CityID: input.CityID, Plan: replicaEntitlementPlan, Status: input.Status, EvidenceDigest: input.EvidenceDigest})
	if err != nil {
		return result, err
	}
	event := nostr.Event{Kind: replicaEntitlementKind, CreatedAt: nostr.Timestamp(input.Now.Unix()), Tags: nostr.Tags{{"d", input.CityID}, {"i", input.CityID}, {"plan", replicaEntitlementPlan}, {"status", input.Status}, {"basis", replicaEntitlementProductionBasis}}, Content: string(content)}
	if err := event.Sign(key); err != nil {
		return result, err
	}
	events = append(events, event)
	data, err := json.MarshalIndent(replicaEntitlementLedgerFile{Version: replicaEntitlementVersion, Events: events}, "", "  ")
	if err != nil {
		return result, err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(input.LedgerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(input.LedgerPath)
		}
	}()
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return result, err
	}
	ledger, err := loadReplicaEntitlementLedger(input.LedgerPath, authority, input.Now)
	if err != nil {
		return result, err
	}
	record, present := ledger.current[input.CityID]
	if !present || record.Event.ID != event.ID || record.Content.Status != input.Status {
		return result, errors.New("production entitlement ledger verification failed")
	}
	keep = true
	return replicaProductionEntitlementIssueResult{CityID: input.CityID, Authority: authority.Hex(), EntitlementEventID: event.ID.Hex(), Status: input.Status, EvidenceDigest: input.EvidenceDigest}, nil
}

func runReplicaProductionEntitlementIssue() error {
	result, err := issueProductionReplicaEntitlement(replicaProductionEntitlementIssueInput{
		AuthorityKeyPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_KEY"),
		SourceLedgerPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_SOURCE"),
		LedgerPath:       os.Getenv("RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_LEDGER"),
		CityID:           os.Getenv("RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_CITY"),
		Status:           os.Getenv("RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_STATUS"),
		EvidenceDigest:   os.Getenv("RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_EVIDENCE"),
		Confirmation:     os.Getenv("RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_CONFIRM"),
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func replicaProductionEntitlementIssueConfigured() bool {
	for _, name := range []string{"RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_KEY", "RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_LEDGER", "RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_CITY", "RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_STATUS", "RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_EVIDENCE", "RELAY_REPLICA_ENTITLEMENT_PRODUCTION_ISSUE_CONFIRM"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

type replicaEntitlementBootstrapInput struct {
	RegistryPath string
	JournalPath  string
	LedgerPath   string
	Authority    nostr.PubKey
	CityID       string
	Destination  string
	Confirmation string
	Now          time.Time
}

type replicaEntitlementBootstrapResult struct {
	CityID             string `json:"cityId"`
	Destination        string `json:"destination"`
	EntitlementEventID string `json:"entitlementEventId"`
	RegistryDigest     string `json:"registryDigest"`
}

func bootstrapEntitledReplicaCity(input replicaEntitlementBootstrapInput) (result replicaEntitlementBootstrapResult, err error) {
	if input.Confirmation != replicaEntitlementBootstrapConfirm || !uuidPattern.MatchString(input.CityID) {
		return result, errors.New("production entitlement bootstrap requires the exact operator scope and confirmation")
	}
	destination, err := normalizeReplicaDestination(input.Destination)
	if err != nil || destination != input.Destination {
		return result, errors.New("production entitlement bootstrap destination is invalid")
	}
	if err := secureEntitlementCandidatePath(input.RegistryPath); err != nil {
		return result, err
	}
	if err := secureEntitlementCandidatePath(input.JournalPath); err != nil {
		return result, err
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	ledger, err := loadReplicaEntitlementLedger(input.LedgerPath, input.Authority, input.Now)
	if err != nil {
		return result, err
	}
	record, active := ledger.active(input.CityID)
	if !active {
		return result, errors.New("production entitlement bootstrap requires a current active entitlement")
	}
	data, err := json.MarshalIndent(replicaRegistryFile{Version: replicaRegistryVersion, Cities: []replicaRegistryCity{{CityID: input.CityID, Destination: destination, EntitlementEventID: record.Event.ID.Hex()}}}, "", "  ")
	if err != nil {
		return result, err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(input.RegistryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(input.RegistryPath)
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(input.RegistryPath)
			_ = os.Remove(input.JournalPath)
		}
	}()
	registry, err := loadReplicaRegistry(input.RegistryPath)
	if err != nil {
		return result, err
	}
	if err := validateEntitledReplicaRegistry(registry, ledger); err != nil {
		return result, err
	}
	journal, err := openReplicaJournal(input.JournalPath, registry, nil)
	if err != nil {
		return result, err
	}
	if err := journal.Close(); err != nil {
		return result, err
	}
	keep = true
	return replicaEntitlementBootstrapResult{CityID: input.CityID, Destination: destination, EntitlementEventID: record.Event.ID.Hex(), RegistryDigest: registry.fingerprint()}, nil
}

func runReplicaEntitlementBootstrap() error {
	authority, err := nostr.PubKeyFromHex(os.Getenv("RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_AUTHORITY"))
	if err != nil {
		return errors.New("invalid production entitlement bootstrap authority")
	}
	result, err := bootstrapEntitledReplicaCity(replicaEntitlementBootstrapInput{
		RegistryPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_REGISTRY"), JournalPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_JOURNAL"), LedgerPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_LEDGER"), Authority: authority, CityID: os.Getenv("RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_CITY"), Destination: os.Getenv("RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_DESTINATION"), Confirmation: os.Getenv("RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_CONFIRM"),
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func replicaEntitlementBootstrapConfigured() bool {
	for _, name := range []string{"RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_REGISTRY", "RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_JOURNAL", "RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_LEDGER", "RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_AUTHORITY", "RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_CITY", "RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_DESTINATION", "RELAY_REPLICA_ENTITLEMENT_BOOTSTRAP_CONFIRM"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}
