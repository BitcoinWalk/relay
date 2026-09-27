package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"fiatjaf.com/nostr"
)

const (
	replicaEntitlementStagingIssueConfirm = "entitlement-staging-issue-v1"
	replicaEntitlementStagingBasis        = "staging-synthetic-v1"
)

type replicaStagingEntitlementIssueInput struct {
	AuthorityKeyPath string
	SourceLedgerPath string
	LedgerPath       string
	CityID           string
	Status           string
	Confirmation     string
	Now              time.Time
}

type replicaStagingEntitlementIssueResult struct {
	CityID             string `json:"cityId"`
	Authority          string `json:"authority"`
	EntitlementEventID string `json:"entitlementEventId"`
	Status             string `json:"status"`
	Synthetic          bool   `json:"synthetic"`
}

func stagingEntitlementEvidenceDigest(cityID string) string {
	digest := sha256.Sum256([]byte(replicaEntitlementStagingBasis + ":" + cityID))
	return hex.EncodeToString(digest[:])
}

func issueStagingReplicaEntitlement(input replicaStagingEntitlementIssueInput) (result replicaStagingEntitlementIssueResult, err error) {
	if input.Confirmation != replicaEntitlementStagingIssueConfirm {
		return result, errors.New("staging entitlement issue requires the exact synthetic confirmation")
	}
	if !uuidPattern.MatchString(input.CityID) {
		return result, errors.New("invalid staging entitlement city")
	}
	if input.Status != replicaEntitlementActive && input.Status != replicaEntitlementRevoked {
		return result, errors.New("invalid staging entitlement status")
	}
	if !filepath.IsAbs(input.AuthorityKeyPath) || filepath.Clean(input.AuthorityKeyPath) != input.AuthorityKeyPath {
		return result, errors.New("staging entitlement authority requires a clean absolute path")
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
		return result, errors.New("relay super-admin cannot issue staging entitlements")
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	events := make([]nostr.Event, 0, 1)
	if input.SourceLedgerPath != "" {
		if filepath.Clean(input.SourceLedgerPath) != input.SourceLedgerPath || !filepath.IsAbs(input.SourceLedgerPath) || input.SourceLedgerPath == input.LedgerPath {
			return result, errors.New("staging entitlement source requires a distinct clean absolute path")
		}
		if _, err := loadReplicaEntitlementLedger(input.SourceLedgerPath, authority, input.Now); err != nil {
			return result, err
		}
		data, err := os.ReadFile(input.SourceLedgerPath)
		if err != nil {
			return result, err
		}
		var source replicaEntitlementLedgerFile
		if err := json.Unmarshal(data, &source); err != nil || len(source.Events) >= 1000 {
			return result, errors.New("invalid staging entitlement source ledger")
		}
		events = append(events, source.Events...)
	}
	content, err := json.Marshal(replicaEntitlementContent{
		Version:        replicaEntitlementVersion,
		CityID:         input.CityID,
		Plan:           replicaEntitlementPlan,
		Status:         input.Status,
		EvidenceDigest: stagingEntitlementEvidenceDigest(input.CityID),
	})
	if err != nil {
		return result, err
	}
	event := nostr.Event{
		Kind:      replicaEntitlementKind,
		CreatedAt: nostr.Timestamp(input.Now.Unix()),
		Tags: nostr.Tags{
			{"d", input.CityID},
			{"i", input.CityID},
			{"plan", replicaEntitlementPlan},
			{"status", input.Status},
			{"basis", replicaEntitlementStagingBasis},
		},
		Content: string(content),
	}
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
		return result, errors.New("staging entitlement ledger verification failed")
	}
	keep = true
	return replicaStagingEntitlementIssueResult{
		CityID: input.CityID, Authority: authority.Hex(), EntitlementEventID: event.ID.Hex(), Status: input.Status, Synthetic: true,
	}, nil
}

func runReplicaStagingEntitlementIssue() error {
	result, err := issueStagingReplicaEntitlement(replicaStagingEntitlementIssueInput{
		AuthorityKeyPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_KEY"),
		SourceLedgerPath: os.Getenv("RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_SOURCE"),
		LedgerPath:       os.Getenv("RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_LEDGER"),
		CityID:           os.Getenv("RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CITY"),
		Status:           os.Getenv("RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_STATUS"),
		Confirmation:     os.Getenv("RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CONFIRM"),
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func replicaStagingEntitlementIssueConfigured() bool {
	for _, name := range []string{
		"RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_KEY",
		"RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_SOURCE",
		"RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_LEDGER",
		"RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CITY",
		"RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_STATUS",
		"RELAY_REPLICA_ENTITLEMENT_STAGING_ISSUE_CONFIRM",
	} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}
