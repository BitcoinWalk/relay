package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func entitlementEvent(t *testing.T, issuer nostr.SecretKey, cityID, status string, createdAt time.Time) nostr.Event {
	t.Helper()
	content, err := json.Marshal(replicaEntitlementContent{
		Version:        replicaEntitlementVersion,
		CityID:         cityID,
		Plan:           replicaEntitlementPlan,
		Status:         status,
		EvidenceDigest: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	event := nostr.Event{
		Kind:      replicaEntitlementKind,
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Tags: nostr.Tags{
			{"d", cityID},
			{"i", cityID},
			{"plan", replicaEntitlementPlan},
			{"status", status},
		},
		Content: string(content),
	}
	if err := event.Sign(issuer); err != nil {
		t.Fatal(err)
	}
	return event
}

func writeEntitlementLedgerFixture(t *testing.T, path string, events []nostr.Event) {
	t.Helper()
	data, err := json.Marshal(replicaEntitlementLedgerFile{Version: replicaEntitlementVersion, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestEntitledReplicaProvisioningProducesOneBoundCandidate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	issuer := nostr.Generate()
	entitlement := entitlementEvent(t, issuer, cityB, replicaEntitlementActive, now.Add(-time.Minute))
	ledgerPath := filepath.Join(dir, "entitlements.json")
	writeEntitlementLedgerFixture(t, ledgerPath, []nostr.Event{entitlement})
	currentPath := filepath.Join(dir, "registry.json")
	writeRegistryFixture(t, currentPath, []replicaRegistryCity{{CityID: cityA, Destination: "wss://one.example/"}})
	candidatePath := filepath.Join(dir, "registry.candidate.json")

	result, err := planEntitledReplicaCity(replicaEntitlementPlanInput{
		CurrentRegistryPath:   currentPath,
		EntitlementLedgerPath: ledgerPath,
		CandidateRegistryPath: candidatePath,
		Authority:             issuer.Public(),
		CityID:                cityB,
		Destination:           "wss://two.example",
		Confirmation:          replicaEntitlementConfirmation,
		Now:                   now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CityID != cityB || result.Destination != "wss://two.example/" || result.EntitlementEventID != entitlement.ID.Hex() || result.PreviousDigest == result.CandidateDigest {
		t.Fatalf("unexpected provisioning result: %+v", result)
	}
	info, err := os.Stat(candidatePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("candidate is not owner-only: %v %v", info, err)
	}
	candidate, err := loadReplicaRegistry(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if destination, ok := candidate.destination(cityB); !ok || destination != "wss://two.example/" {
		t.Fatal("candidate omitted the entitled destination")
	}
	if eventID, ok := candidate.entitlement(cityB); !ok || eventID != entitlement.ID.Hex() {
		t.Fatal("candidate was not bound to the exact entitlement event")
	}

	current, err := loadReplicaRegistry(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(dir, "journal.db")
	journal, err := openReplicaJournal(journalPath, current, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := addReplicaRegistryCity(journalPath, currentPath, candidatePath, replicaRegistryAddConfirmation); err == nil {
		t.Fatal("legacy additive migration accepted an entitlement-bound candidate")
	}
	if _, err := applyEntitledReplicaCity(replicaEntitlementApplyInput{
		JournalPath: journalPath, CurrentRegistryPath: currentPath, EntitlementLedgerPath: ledgerPath, CandidateRegistryPath: candidatePath,
		Authority: issuer.Public(), CityID: cityB, Confirmation: replicaEntitlementApplyConfirm, Now: now,
	}); err != nil {
		t.Fatalf("entitlement apply rejected the bound candidate: %v", err)
	}
}

func TestEntitledReplicaProvisioningRejectsNonEntitlementsAndRevocation(t *testing.T) {
	now := time.Unix(1800000000, 0)
	issuer := nostr.Generate()
	valid := entitlementEvent(t, issuer, cityB, replicaEntitlementActive, now.Add(-time.Minute))
	revoked := entitlementEvent(t, issuer, cityB, replicaEntitlementRevoked, now)
	wrongKind := valid
	wrongKind.Kind = 30301
	if err := wrongKind.Sign(issuer); err != nil {
		t.Fatal(err)
	}
	tampered := valid
	tampered.Content = strings.Replace(tampered.Content, replicaEntitlementActive, replicaEntitlementRevoked, 1)
	future := entitlementEvent(t, issuer, cityB, replicaEntitlementActive, now.Add(6*time.Minute))
	wrongTag := entitlementEvent(t, issuer, cityB, replicaEntitlementActive, now.Add(-time.Minute))
	wrongTag.Tags[1][1] = cityA
	if err := wrongTag.Sign(issuer); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name         string
		events       []nostr.Event
		authority    nostr.PubKey
		confirmation string
	}{
		{"requested tier event", []nostr.Event{wrongKind}, issuer.Public(), replicaEntitlementConfirmation},
		{"tampered event", []nostr.Event{tampered}, issuer.Public(), replicaEntitlementConfirmation},
		{"wrong issuer", []nostr.Event{valid}, nostr.Generate().Public(), replicaEntitlementConfirmation},
		{"future event", []nostr.Event{future}, issuer.Public(), replicaEntitlementConfirmation},
		{"scope mismatch", []nostr.Event{wrongTag}, issuer.Public(), replicaEntitlementConfirmation},
		{"revoked", []nostr.Event{valid, revoked}, issuer.Public(), replicaEntitlementConfirmation},
		{"wrong confirmation", []nostr.Event{valid}, issuer.Public(), "paid-requested"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			currentPath := filepath.Join(dir, "registry.json")
			writeRegistryFixture(t, currentPath, []replicaRegistryCity{{CityID: cityA, Destination: "wss://one.example/"}})
			ledgerPath := filepath.Join(dir, "entitlements.json")
			writeEntitlementLedgerFixture(t, ledgerPath, test.events)
			candidatePath := filepath.Join(dir, "candidate.json")
			_, err := planEntitledReplicaCity(replicaEntitlementPlanInput{
				CurrentRegistryPath: currentPath, EntitlementLedgerPath: ledgerPath, CandidateRegistryPath: candidatePath,
				Authority: test.authority, CityID: cityB, Destination: "wss://two.example/", Confirmation: test.confirmation, Now: now,
			})
			if err == nil {
				t.Fatal("unsafe provisioning input was accepted")
			}
			if _, statErr := os.Lstat(candidatePath); !os.IsNotExist(statErr) {
				t.Fatalf("failed provisioning retained a candidate: %v", statErr)
			}
		})
	}
}

func TestEntitlementRequiredRegistryFailsClosed(t *testing.T) {
	now := time.Unix(1800000000, 0)
	issuer := nostr.Generate()
	active := entitlementEvent(t, issuer, cityB, replicaEntitlementActive, now.Add(-time.Minute))
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "entitlements.json")
	writeEntitlementLedgerFixture(t, ledgerPath, []nostr.Event{active})
	ledger, err := loadReplicaEntitlementLedger(ledgerPath, issuer.Public(), now)
	if err != nil {
		t.Fatal(err)
	}
	registry := &replicaRegistry{
		destinations: map[string]string{cityB: "wss://two.example/"},
		entitlements: map[string]string{cityB: active.ID.Hex()},
	}
	if err := validateEntitledReplicaRegistry(registry, ledger); err != nil {
		t.Fatalf("active exact entitlement rejected: %v", err)
	}
	registry.entitlements[cityB] = strings.Repeat("b", 64)
	if err := validateEntitledReplicaRegistry(registry, ledger); err == nil {
		t.Fatal("registry accepted a different entitlement event")
	}
	delete(registry.entitlements, cityB)
	if err := validateEntitledReplicaRegistry(registry, ledger); err == nil {
		t.Fatal("entitlement-required registry accepted an unbound city")
	}
	writeEntitlementLedgerFixture(t, ledgerPath, []nostr.Event{active, entitlementEvent(t, issuer, cityB, replicaEntitlementRevoked, now)})
	revokedLedger, err := loadReplicaEntitlementLedger(ledgerPath, issuer.Public(), now)
	if err != nil {
		t.Fatal(err)
	}
	registry.entitlements[cityB] = active.ID.Hex()
	if err := validateEntitledReplicaRegistry(registry, revokedLedger); err == nil {
		t.Fatal("registry accepted an entitlement superseded by revocation")
	}
}

func TestReplicaEntitlementRuntimeConfigurationCannotBeIgnored(t *testing.T) {
	for _, name := range []string{"RELAY_REPLICA_REQUIRE_ENTITLEMENTS", "RELAY_REPLICA_ENTITLEMENT_LEDGER", "RELAY_REPLICA_ENTITLEMENT_AUTHORITY", "RELAY_ORGANIZER_MODE", "RELAY_REPLICA_JOURNAL", "RELAY_REPLICA_REGISTRY"} {
		t.Setenv(name, "")
	}
	t.Setenv("RELAY_REPLICA_REQUIRE_ENTITLEMENTS", "true")
	if err := run(); err == nil || !strings.Contains(err.Error(), "requires organizer mode, journal and registry") {
		t.Fatalf("partial runtime entitlement configuration was ignored: %v", err)
	}
}

func TestEntitlementProvisioningRequiresOwnerControlledNonOverwritingFiles(t *testing.T) {
	now := time.Unix(1800000000, 0)
	issuer := nostr.Generate()
	active := entitlementEvent(t, issuer, cityB, replicaEntitlementActive, now)
	makeInput := func(t *testing.T) (replicaEntitlementPlanInput, string) {
		t.Helper()
		dir := t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		current := filepath.Join(dir, "registry.json")
		writeRegistryFixture(t, current, []replicaRegistryCity{{CityID: cityA, Destination: "wss://one.example/"}})
		ledger := filepath.Join(dir, "entitlements.json")
		writeEntitlementLedgerFixture(t, ledger, []nostr.Event{active})
		return replicaEntitlementPlanInput{
			CurrentRegistryPath: current, EntitlementLedgerPath: ledger, CandidateRegistryPath: filepath.Join(dir, "candidate.json"),
			Authority: issuer.Public(), CityID: cityB, Destination: "wss://two.example/", Confirmation: replicaEntitlementConfirmation, Now: now,
		}, ledger
	}
	t.Run("writable ledger", func(t *testing.T) {
		input, ledger := makeInput(t)
		if err := os.Chmod(ledger, 0666); err != nil {
			t.Fatal(err)
		}
		if _, err := planEntitledReplicaCity(input); err == nil {
			t.Fatal("group/world-writable entitlement ledger was accepted")
		}
	})
	t.Run("ledger symlink", func(t *testing.T) {
		input, ledger := makeInput(t)
		link := ledger + ".link"
		if err := os.Symlink(ledger, link); err != nil {
			t.Fatal(err)
		}
		input.EntitlementLedgerPath = link
		if _, err := planEntitledReplicaCity(input); err == nil {
			t.Fatal("symlink entitlement ledger was accepted")
		}
	})
	t.Run("existing candidate", func(t *testing.T) {
		input, _ := makeInput(t)
		const sentinel = "do not overwrite"
		if err := os.WriteFile(input.CandidateRegistryPath, []byte(sentinel), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := planEntitledReplicaCity(input); err == nil {
			t.Fatal("existing candidate was accepted")
		}
		data, err := os.ReadFile(input.CandidateRegistryPath)
		if err != nil || string(data) != sentinel {
			t.Fatalf("existing candidate changed: %q %v", data, err)
		}
	})
}

func TestEntitlementApplyRechecksRevocationBeforeJournalMutation(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	issuer := nostr.Generate()
	active := entitlementEvent(t, issuer, cityB, replicaEntitlementActive, now.Add(-time.Minute))
	ledgerPath := filepath.Join(dir, "entitlements.json")
	writeEntitlementLedgerFixture(t, ledgerPath, []nostr.Event{active})
	currentPath := filepath.Join(dir, "registry.json")
	writeRegistryFixture(t, currentPath, []replicaRegistryCity{{CityID: cityA, Destination: "wss://one.example/"}})
	current, err := loadReplicaRegistry(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(dir, "journal.db")
	journal, err := openReplicaJournal(journalPath, current, nil)
	if err != nil {
		t.Fatal(err)
	}
	journal.Close()
	candidatePath := filepath.Join(dir, "candidate.json")
	if _, err := planEntitledReplicaCity(replicaEntitlementPlanInput{
		CurrentRegistryPath: currentPath, EntitlementLedgerPath: ledgerPath, CandidateRegistryPath: candidatePath,
		Authority: issuer.Public(), CityID: cityB, Destination: "wss://two.example/", Confirmation: replicaEntitlementConfirmation, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	revoked := entitlementEvent(t, issuer, cityB, replicaEntitlementRevoked, now)
	writeEntitlementLedgerFixture(t, ledgerPath, []nostr.Event{active, revoked})
	if _, err := applyEntitledReplicaCity(replicaEntitlementApplyInput{
		JournalPath: journalPath, CurrentRegistryPath: currentPath, EntitlementLedgerPath: ledgerPath, CandidateRegistryPath: candidatePath,
		Authority: issuer.Public(), CityID: cityB, Confirmation: replicaEntitlementApplyConfirm, Now: now,
	}); err == nil {
		t.Fatal("revoked entitlement was applied after planning")
	}
	reopened, err := openReplicaJournal(journalPath, current, nil)
	if err != nil {
		t.Fatalf("rejected entitlement changed the journal: %v", err)
	}
	reopened.Close()
}

func TestStagingEntitlementIssuerCreatesOneSyntheticOwnerOnlyLedger(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "staging-authority-key")
	pubkey, err := initializeReplicaServiceKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(dir, "staging-entitlements.json")
	now := time.Unix(1800000000, 0)
	result, err := issueStagingReplicaEntitlement(replicaStagingEntitlementIssueInput{
		AuthorityKeyPath: keyPath,
		LedgerPath:       ledgerPath,
		CityID:           cityB,
		Status:           replicaEntitlementActive,
		Confirmation:     replicaEntitlementStagingIssueConfirm,
		Now:              now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CityID != cityB || result.Authority != pubkey.Hex() || len(result.EntitlementEventID) != 64 || result.Status != replicaEntitlementActive || result.Synthetic != true {
		t.Fatalf("unexpected staging entitlement result: %+v", result)
	}
	info, err := os.Stat(ledgerPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("staging ledger is not owner-only: %v %v", info, err)
	}
	ledger, err := loadReplicaEntitlementLedger(ledgerPath, pubkey, now)
	if err != nil {
		t.Fatal(err)
	}
	record, active := ledger.active(cityB)
	if !active || record.Event.ID.Hex() != result.EntitlementEventID {
		t.Fatal("issued staging entitlement was not the exact active ledger event")
	}
	if record.Content.EvidenceDigest != stagingEntitlementEvidenceDigest(cityB) {
		t.Fatal("staging evidence commitment is not the fixed synthetic commitment")
	}
	if !exactEntitlementTag(record.Event, "basis", replicaEntitlementStagingBasis) {
		t.Fatal("staging entitlement is not explicitly tagged as synthetic")
	}
	revokedPath := filepath.Join(dir, "staging-entitlements-revoked.json")
	revokedResult, err := issueStagingReplicaEntitlement(replicaStagingEntitlementIssueInput{
		AuthorityKeyPath: keyPath, SourceLedgerPath: ledgerPath, LedgerPath: revokedPath,
		CityID: cityB, Status: replicaEntitlementRevoked,
		Confirmation: replicaEntitlementStagingIssueConfirm, Now: now.Add(time.Second),
	})
	if err != nil || revokedResult.Status != replicaEntitlementRevoked {
		t.Fatalf("staging issuer could not create an immutable revoked successor: %+v %v", revokedResult, err)
	}
	revokedLedger, err := loadReplicaEntitlementLedger(revokedPath, pubkey, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, active := revokedLedger.active(cityB); active {
		t.Fatal("revoked successor ledger retained an active entitlement")
	}
	data, err := os.ReadFile(revokedPath)
	if err != nil {
		t.Fatal(err)
	}
	var successor replicaEntitlementLedgerFile
	if err := json.Unmarshal(data, &successor); err != nil || len(successor.Events) != 2 {
		t.Fatalf("successor ledger did not retain immutable history: %v %v", len(successor.Events), err)
	}
	if _, err := issueStagingReplicaEntitlement(replicaStagingEntitlementIssueInput{
		AuthorityKeyPath: keyPath, LedgerPath: ledgerPath, CityID: cityB, Status: replicaEntitlementActive,
		Confirmation: replicaEntitlementStagingIssueConfirm, Now: now,
	}); err == nil {
		t.Fatal("staging issuer overwrote an existing ledger")
	}
}

func TestStagingEntitlementIssuerRejectsUnsafeInputsWithoutLedger(t *testing.T) {
	for _, test := range []struct {
		name         string
		cityID       string
		status       string
		confirmation string
		unsafeParent bool
	}{
		{name: "invalid city", cityID: "not-a-city", status: replicaEntitlementActive, confirmation: replicaEntitlementStagingIssueConfirm},
		{name: "invalid status", cityID: cityB, status: "paid", confirmation: replicaEntitlementStagingIssueConfirm},
		{name: "wrong confirmation", cityID: cityB, status: replicaEntitlementActive, confirmation: "payment-received"},
		{name: "writable parent", cityID: cityB, status: replicaEntitlementActive, confirmation: replicaEntitlementStagingIssueConfirm, unsafeParent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			mode := os.FileMode(0700)
			if test.unsafeParent {
				mode = 0777
			}
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			keyPath := filepath.Join(t.TempDir(), "staging-authority-key")
			if _, err := initializeReplicaServiceKey(keyPath); err != nil {
				t.Fatal(err)
			}
			ledgerPath := filepath.Join(dir, "ledger.json")
			_, err := issueStagingReplicaEntitlement(replicaStagingEntitlementIssueInput{
				AuthorityKeyPath: keyPath, LedgerPath: ledgerPath, CityID: test.cityID, Status: test.status,
				Confirmation: test.confirmation, Now: time.Unix(1800000000, 0),
			})
			if err == nil {
				t.Fatal("unsafe staging entitlement input was accepted")
			}
			if _, statErr := os.Lstat(ledgerPath); !os.IsNotExist(statErr) {
				t.Fatalf("rejected staging issue retained a ledger: %v", statErr)
			}
		})
	}
}

func TestEntitlementRehearsalUsesPublicStateInsteadOfMutableJournalDigest(t *testing.T) {
	data, err := os.ReadFile("deploy/rehearse-entitlement-provisioning-0.8.32.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if strings.Contains(script, "journal_digest_before") || strings.Contains(script, "journal_digest_after") {
		t.Fatal("rehearsal compares a normally mutable live journal")
	}
	for _, required := range []string{"memphis-before.json", "nashville-before.json", "memphis-after.json", "nashville-after.json", "cmp \"$backup/memphis-before.json\" \"$backup/memphis-after.json\"", "cmp \"$backup/nashville-before.json\" \"$backup/nashville-after.json\""} {
		if !strings.Contains(script, required) {
			t.Fatalf("rehearsal omits public-state assertion %q", required)
		}
	}
}

func TestEntitlementRehearsalWaitsForSourceAfterFinalAudit(t *testing.T) {
	data, err := os.ReadFile("deploy/rehearse-entitlement-provisioning-0.8.32.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if strings.Count(script, "wait_source_health") < 2 {
		t.Fatal("source backup restart does not use the bounded readiness wait")
	}
}
