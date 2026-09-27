package citygroups

import (
	"encoding/json"
	"reflect"
	"testing"

	"fiatjaf.com/nostr"
)

const testCity = "66f137cb-2ac1-4eef-8358-7dd66b45922f"

func grant(t *testing.T, admin nostr.SecretKey, creator nostr.PubKey, editors []nostr.PubKey, when nostr.Timestamp) nostr.Event {
	t.Helper()
	keys := []string{}
	for _, key := range editors {
		keys = append(keys, key.Hex())
	}
	content, _ := json.Marshal(LegacyGrant{CityID: testCity, CreatorPubkey: creator.Hex(), CreatorRevisionID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", EditorPubkeys: keys, SuperAdminPubkey: nostr.GetPublicKey(admin).Hex()})
	event := nostr.Event{Kind: 30302, CreatedAt: when, Tags: nostr.Tags{{"d", testCity}}, Content: string(content)}
	if err := event.Sign(admin); err != nil {
		t.Fatal(err)
	}
	return event
}
func TestRevocationPreservedInMigration(t *testing.T) {
	admin := nostr.Generate()
	creator := nostr.GetPublicKey(nostr.Generate())
	editor := nostr.GetPublicKey(nostr.Generate())
	older := grant(t, admin, creator, []nostr.PubKey{creator, editor}, 100)
	newer := grant(t, admin, creator, []nostr.PubKey{creator}, 101)
	seeds, err := SelectSeeds([]nostr.Event{newer, older}, nostr.GetPublicKey(admin))
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != 1 || len(seeds[0].Editors) != 1 || seeds[0].Editors[0] != creator {
		t.Fatal("migration restored a removed editor")
	}
	if seeds[0].CityID != testCity || seeds[0].SourceID != newer.ID {
		t.Fatal("lost stable city/source identity")
	}
}
func TestSignedMetadataAndSeparation(t *testing.T) {
	admin, relay := nostr.Generate(), nostr.Generate()
	creator := nostr.GetPublicKey(nostr.Generate())
	editor := nostr.GetPublicKey(nostr.Generate())
	seed, err := SeedFromGrant(grant(t, admin, creator, []nostr.PubKey{creator, editor}, 100), nostr.GetPublicKey(admin))
	if err != nil {
		t.Fatal(err)
	}
	events, err := seed.Metadata("wss://example.com", relay, 200)
	if err != nil {
		t.Fatal(err)
	}
	again, err := seed.Metadata("wss://example.com", relay, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, again) {
		t.Fatal("metadata must be deterministic")
	}
	for i, event := range events {
		if event.Kind != nostr.Kind(39000+i) || event.Tags.GetD() != testCity || event.PubKey != nostr.GetPublicKey(relay) || !event.CheckID() || !event.VerifySignature() {
			t.Fatal("invalid group metadata")
		}
	}
	group := seed.Group("wss://example.com", nostr.GetPublicKey(relay), 200)
	if group.Private || group.Hidden || !group.Closed || !group.Restricted {
		t.Fatal("unexpected group visibility")
	}
	if group.Members[creator][0].Name != Owner || group.Members[editor][0].Name != Organizer {
		t.Fatal("incorrect role mapping")
	}
	if _, err := seed.Metadata("wss://example.com", admin, 200); err == nil {
		t.Fatal("personal admin key used as relay key")
	}
}
func TestAuditRejectsTamperingAndMalformedAuthority(t *testing.T) {
	admin := nostr.Generate()
	creator := nostr.GetPublicKey(nostr.Generate())
	pk := nostr.GetPublicKey(admin)
	original := grant(t, admin, creator, []nostr.PubKey{creator}, 100)
	bad := original
	bad.Content += " "
	if _, err := SeedFromGrant(bad, pk); err == nil {
		t.Fatal("tampered event accepted")
	}
	bad = original
	bad.Tags = nostr.Tags{{"d", testCity}, {"d", testCity}}
	bad.Sign(admin)
	if _, err := SelectSeeds([]nostr.Event{bad}, pk); err == nil {
		t.Fatal("malformed grant silently accepted")
	}
	bad = original
	bad.Tags = nostr.Tags{{"d", testCity}, {"expiration", "9999999999"}}
	bad.Sign(admin)
	if _, err := SeedFromGrant(bad, pk); err == nil {
		t.Fatal("expiring grant accepted")
	}
	outsider := nostr.Generate()
	if _, err := SeedFromGrant(original, nostr.GetPublicKey(outsider)); err == nil {
		t.Fatal("wrong signer accepted")
	}
}
