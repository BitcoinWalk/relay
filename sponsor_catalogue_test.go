package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
)

func catalogueEvent(t *testing.T, sk nostr.SecretKey, value sponsorCatalogueRecord, offset int) nostr.Event {
	t.Helper()
	body, _ := json.Marshal(value)
	marker := value.State
	if value.RecordType == "approval" || value.RecordType == "intake" {
		marker = value.Status
	}
	tags := nostr.Tags{{"d", sponsorCatalogueID + ":" + value.RecordType + ":" + value.SponsorPubkey + fmt.Sprintf(":00000000-0000-4000-8000-%012d", offset)}, {"i", sponsorCatalogueID}, {"p", value.SponsorPubkey}, {"record", value.RecordType}, {"state", marker}, {"client", "bitcoinwalk.org"}}
	if value.PreviousRevisionID != "" {
		tags = append(tags, nostr.Tag{"e", value.PreviousRevisionID, "", "previous"})
	}
	if value.RecordType == "approval" {
		tags = append(tags, nostr.Tag{"e", value.SourceEventID, "", "source"})
	}
	event := nostr.Event{Kind: sponsorCatalogueKind, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Tags: tags, Content: string(body)}
	if err := event.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return event
}

func approvedSponsorAssignment(t *testing.T, admin nostr.SecretKey, sponsor string) nostr.Event {
	t.Helper()
	value := sponsorship{Version: 2, Scope: sponsorshipScope{Type: "city", CityID: cityA}, Mode: "sponsor", SponsorPubkey: sponsor, Website: "https://teamtoxic.example/", LogoHash: "b" + strings.Repeat("0", 63)}
	body, _ := json.Marshal(value)
	key := sponsorshipKey(value.Scope)
	event := nostr.Event{Kind: sponsorshipKind, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"d", sponsorshipID + ":" + key + ":00000000-0000-4000-8000-000000000001"}, {"i", sponsorshipID}, {"scope", key}, {"mode", "sponsor"}, {"client", "bitcoinwalk.org"}}, Content: string(body)}
	if err := event.Sign(admin); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestSponsorCatalogueImportsExistingApprovedSponsorWithoutChangingAssignment(t *testing.T) {
	admin, sponsor, outsider := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, sponsorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(sponsor)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	assignment := approvedSponsorAssignment(t, admin, sponsorPK.Hex())
	accept(t, relay, assignment)
	approval := sponsorCatalogueRecord{Version: 1, RecordType: "approval", SponsorPubkey: sponsorPK.Hex(), Status: "approved", DisplayName: "TeamToxic", Website: "https://teamtoxic.example/", LogoHash: "b" + strings.Repeat("0", 63), Source: "legacy-assignment", SourceEventID: assignment.ID.Hex(), SourceCityID: cityA, SourceScopeKey: "city:" + cityA}
	imported := catalogueEvent(t, admin, approval, 1)
	accept(t, relay, imported)
	accept(t, relay, imported)
	deny(t, relay, catalogueEvent(t, outsider, approval, 2))
	bad := approval
	bad.LogoHash = "c" + strings.Repeat("0", 63)
	deny(t, relay, catalogueEvent(t, admin, bad, 3))
	if stored := policy.byID(assignment.ID.Hex()); stored == nil {
		t.Fatal("source assignment changed or disappeared")
	}
}

func TestSponsorCatalogueClaimRequiresSponsorAndExactSuccessor(t *testing.T) {
	admin, sponsor, outsider := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, sponsorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(sponsor)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	claim := sponsorCatalogueRecord{Version: 1, RecordType: "claim", SponsorPubkey: sponsorPK.Hex(), State: "active", DisplayName: "TeamToxic", Website: "https://teamtoxic.example/"}
	deny(t, relay, catalogueEvent(t, outsider, claim, 0))
	firstWithdrawal := claim
	firstWithdrawal.State = "withdrawn"
	deny(t, relay, catalogueEvent(t, sponsor, firstWithdrawal, 1))
	first := catalogueEvent(t, sponsor, claim, 1)
	accept(t, relay, first)
	claim.State = "withdrawn"
	deny(t, relay, catalogueEvent(t, sponsor, claim, 2))
	claim.PreviousRevisionID = first.ID.Hex()
	accept(t, relay, catalogueEvent(t, sponsor, claim, 3))
}

func TestSponsorCatalogueCannotApproveAClaimAfterSponsorWithdraws(t *testing.T) {
	admin, sponsor := nostr.Generate(), nostr.Generate()
	adminPK, sponsorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(sponsor)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	assignment := approvedSponsorAssignment(t, admin, sponsorPK.Hex())
	accept(t, relay, assignment)
	claim := sponsorCatalogueRecord{Version: 1, RecordType: "claim", SponsorPubkey: sponsorPK.Hex(), State: "active", DisplayName: "TeamToxic", Website: "https://teamtoxic.example/"}
	active := catalogueEvent(t, sponsor, claim, 1)
	accept(t, relay, active)
	claim.State, claim.PreviousRevisionID = "withdrawn", active.ID.Hex()
	accept(t, relay, catalogueEvent(t, sponsor, claim, 2))
	approval := sponsorCatalogueRecord{Version: 1, RecordType: "approval", SponsorPubkey: sponsorPK.Hex(), Status: "approved", DisplayName: "TeamToxic", Website: "https://teamtoxic.example/", LogoHash: "b" + strings.Repeat("0", 63), Source: "self-claim", SourceEventID: active.ID.Hex(), SourceCityID: cityA, SourceScopeKey: "city:" + cityA}
	deny(t, relay, catalogueEvent(t, admin, approval, 3))
}

func TestSponsorCatalogueManualIntakeIsPendingOnly(t *testing.T) {
	admin, sponsor, outsider := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, sponsorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(sponsor)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	intake := sponsorCatalogueRecord{Version: 1, RecordType: "intake", SponsorPubkey: sponsorPK.Hex(), Status: "pending", DisplayName: "Pending sponsor", Website: "https://pending.example/"}
	deny(t, relay, catalogueEvent(t, outsider, intake, 1))
	first := catalogueEvent(t, admin, intake, 2)
	accept(t, relay, first)
	intake.DisplayName, intake.PreviousRevisionID = "Updated pending sponsor", first.ID.Hex()
	accept(t, relay, catalogueEvent(t, admin, intake, 3))
}

func TestSponsorCatalogueReplacementArtworkRequiresCurrentClaimAndApprovalSuccessor(t *testing.T) {
	admin, sponsor := nostr.Generate(), nostr.Generate()
	adminPK, sponsorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(sponsor)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	claim := sponsorCatalogueRecord{Version: 1, RecordType: "claim", SponsorPubkey: sponsorPK.Hex(), State: "active", DisplayName: "TeamToxic", Website: "https://teamtoxic.example/"}
	active := catalogueEvent(t, sponsor, claim, 1)
	accept(t, relay, active)
	approval := sponsorCatalogueRecord{Version: 1, RecordType: "approval", SponsorPubkey: sponsorPK.Hex(), Status: "approved", DisplayName: claim.DisplayName, Website: claim.Website, LogoHash: "b" + strings.Repeat("0", 63), Source: "self-claim", SourceEventID: active.ID.Hex(), SourceCityID: sponsorCatalogueAssetCityID, SourceScopeKey: "catalogue:" + sponsorPK.Hex()}
	first := catalogueEvent(t, admin, approval, 2)
	accept(t, relay, first)
	approval.LogoHash, approval.PreviousRevisionID = "c"+strings.Repeat("0", 63), first.ID.Hex()
	second := catalogueEvent(t, admin, approval, 3)
	accept(t, relay, second)
	stale := approval
	stale.LogoHash = "d" + strings.Repeat("0", 63)
	deny(t, relay, catalogueEvent(t, admin, stale, 4))
	wrongScope := approval
	wrongScope.PreviousRevisionID = second.ID.Hex()
	wrongScope.SourceScopeKey = "city:" + cityA
	deny(t, relay, catalogueEvent(t, admin, wrongScope, 5))
}
