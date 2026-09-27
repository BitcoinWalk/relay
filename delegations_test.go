package main

import (
	"encoding/json"
	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func delegationFixture(t *testing.T, relay *khatru.Relay, admin, creator nostr.SecretKey) (nostr.Event, cityGrant) {
	creatorPK, adminPK := nostr.GetPublicKey(creator), nostr.GetPublicKey(admin)
	revision := draftEvent(t, creator, cityA, 0)
	accept(t, relay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, relay, approval)
	city, _ := parseDraft(revision)
	start := time.Now().Add(48 * time.Hour).Unix()
	walk := nostr.Event{Kind: 31923, CreatedAt: nostr.Now(), Content: city.Description, Tags: nostr.Tags{
		{"d", "123e4567-e89b-42d3-a456-426614174000:2026-09-22"}, {"title", "BitcoinWalk " + city.CityName}, {"summary", "BitcoinWalk in " + city.CityName}, {"image", city.HeroImageURL},
		{"start", strconv.FormatInt(start, 10)}, {"D", strconv.FormatInt(start/86400, 10)}, {"location", "Outside the library"}, {"location", "1.5,2.5"}, {"t", "bitcoinwalk"}, {"r", city.ChatURL},
		{"end", strconv.FormatInt(start+3600, 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "occurrence-v1"},
		{"e", revision.ID.Hex(), "", "city-revision"}, {"e", approval.ID.Hex(), "", "city-approval"},
	}}
	walk.Sign(creator)
	accept(t, relay, walk)
	return walk, grant
}
func walkInvite(t *testing.T, key nostr.SecretKey, walk nostr.Event, nominee nostr.PubKey, action, previous string, offset int) nostr.Event {
	expires := nostr.Timestamp(0)
	if action == "invite" {
		expires = nostr.Now() + 86400
	}
	return workflowEvent(t, key, delegationKind, cityDelegation{Version: 2, CityID: cityA, EventID: walk.ID.Hex(), Nominee: nominee.Hex(), Action: action, PreviousID: previous, ExpiresAt: expires}, nostr.Tags{{"d", cityA + fmt.Sprintf(":00000000-0000-4000-8000-%012d", offset)}, {"i", cityA}, {"e", walk.ID.Hex()}, {"p", nominee.Hex()}, {"action", action}}, offset)
}
func walkAccept(t *testing.T, key nostr.SecretKey, invite, walk nostr.Event, offset int) nostr.Event {
	return workflowEvent(t, key, delegationAcceptanceKind, delegationAcceptance{Version: 2, CityID: cityA, EventID: walk.ID.Hex(), InvitationID: invite.ID.Hex()}, nostr.Tags{{"d", invite.ID.Hex()}, {"i", cityA}, {"e", invite.ID.Hex()}, {"walk", walk.ID.Hex()}}, offset)
}
func TestWalkDelegationLifecycle(t *testing.T) {
	admin, creator, nominee, stranger := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, nomineePK := nostr.GetPublicKey(admin), nostr.GetPublicKey(nominee)
	path := filepath.Join(t.TempDir(), "events.db")
	relay, db, err := newRelay(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	walk, grant := delegationFixture(t, relay, admin, creator)
	deny(t, relay, walkInvite(t, stranger, walk, nomineePK, "invite", "", 3))
	invite := walkInvite(t, creator, walk, nomineePK, "invite", "", 3)
	accept(t, relay, invite)
	deny(t, relay, walkAccept(t, stranger, invite, walk, 4))
	wrong := walkAccept(t, nominee, invite, walk, 4)
	wrong.Tags[3] = nostr.Tag{"walk", fmt.Sprintf("%064d", 1)}
	wrong.Sign(nominee)
	deny(t, relay, wrong)
	wrongCity := walkAccept(t, nominee, invite, walk, 4)
	wrongCity.Tags[1] = nostr.Tag{"i", cityB}
	wrongCity.Sign(nominee)
	deny(t, relay, wrongCity)
	accepted := walkAccept(t, nominee, invite, walk, 4)
	accept(t, relay, accepted)
	if policy.canEdit(nomineePK, &grant) {
		t.Fatal("single-walk acceptance granted city editing")
	}
	deny(t, relay, draftEvent(t, nominee, cityA, 5))
	deny(t, relay, walkInvite(t, nominee, walk, nostr.GetPublicKey(stranger), "invite", invite.ID.Hex(), 5))
	deletion := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 5, Tags: nostr.Tags{{"e", walk.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: delegate"}
	deletion.Sign(nominee)
	deny(t, relay, deletion)
	// Another occurrence cannot reuse this invitation, even with matching city and signer.
	other := walk
	other.Tags = append(nostr.Tags{}, walk.Tags...)
	other.Tags[0] = nostr.Tag{"d", "123e4567-e89b-42d3-a456-426614174001:2026-09-23"}
	other.Sign(creator)
	accept(t, relay, other)
	deny(t, relay, walkAccept(t, nominee, invite, other, 5))
	if policy.latestWalkDelegation(other) != nil {
		t.Fatal("delegation leaked to another occurrence")
	}
	revoke := walkInvite(t, admin, walk, nomineePK, "revoke", invite.ID.Hex(), 6)
	accept(t, relay, revoke)
	deny(t, relay, invite)
	deny(t, relay, accepted)
	next := walkInvite(t, creator, walk, nomineePK, "invite", revoke.ID.Hex(), 7)
	accept(t, relay, next)
	prepared := walkAccept(t, nominee, next, walk, 8)
	if err := policy.check(authCtx(t, prepared), prepared); err != nil {
		t.Fatal(err)
	}
	revoke2 := walkInvite(t, creator, walk, nomineePK, "revoke", next.ID.Hex(), 9)
	accept(t, relay, revoke2)
	if err := relay.ReplaceEvent(authCtx(t, prepared), prepared); err == nil {
		t.Fatal("acceptance raced revocation")
	}
	// Version 1 city-wide invitations are rejected and never grant city access.
	legacy := walkInvite(t, creator, walk, nomineePK, "invite", revoke2.ID.Hex(), 10)
	var c cityDelegation
	json.Unmarshal([]byte(legacy.Content), &c)
	c.Version = 1
	b, _ := json.Marshal(c)
	legacy.Content = string(b)
	legacy.Sign(creator)
	deny(t, relay, legacy)
	if err := db.SaveEvent(legacy); err != nil {
		t.Fatal(err)
	} // historical fixture
	if policy.canEdit(nomineePK, &grant) {
		t.Fatal("legacy invitation granted city access")
	}
	// Expiry cannot be bypassed with a backdated acceptance.
	expired := walkInvite(t, creator, other, nomineePK, "invite", "", 11)
	json.Unmarshal([]byte(expired.Content), &c)
	c.ExpiresAt = nostr.Now() - 10
	expired.CreatedAt = nostr.Now() - 100
	b, _ = json.Marshal(c)
	expired.Content = string(b)
	expired.Sign(creator)
	db.SaveEvent(expired)
	deny(t, relay, walkAccept(t, nominee, expired, other, -20))
	// Cancelling the occurrence blocks further acceptance.
	pending := walkInvite(t, creator, walk, nomineePK, "invite", legacy.ID.Hex(), 12)
	accept(t, relay, pending)
	deletion.Sign(creator)
	accept(t, relay, deletion)
	deny(t, relay, walkAccept(t, nominee, pending, walk, 13))
	relay.DisableExpirationManager()
	db.Close()
	relay, db, err = newRelay(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy = unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	if policy.canEdit(nomineePK, &grant) {
		t.Fatal("restart granted city access")
	}
	deny(t, relay, accepted)
}
