// Package citygroups defines the migration boundary between BitcoinWalk city
// permissions and NIP-29. It does not enable NIP-29 on a running relay.
package citygroups

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip29"
)

const Owner = "owner"
const Organizer = "organizer"
const SuperAdmin = "super-admin"

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type LegacyGrant struct {
	CityID            string   `json:"cityId"`
	CreatorPubkey     string   `json:"creatorPubkey"`
	CreatorRevisionID string   `json:"creatorRevisionId"`
	EditorPubkeys     []string `json:"editorPubkeys"`
	SuperAdminPubkey  string   `json:"superAdminPubkey"`
}

type Seed struct {
	CityID            string          `json:"cityId"`
	Creator           nostr.PubKey    `json:"creator"`
	CreatorRevisionID string          `json:"creatorRevisionId"`
	SourceID          nostr.ID        `json:"sourceAuthorizationId"`
	SourceTimestamp   nostr.Timestamp `json:"sourceTimestamp"`
	Admin             nostr.PubKey    `json:"admin"`
	Editors           []nostr.PubKey  `json:"editors"`
}

// SeedFromGrant accepts only a signature-verified authorization from the exact
// organizational admin. It never learns privileges from relay metadata or an npub label.
func SeedFromGrant(event nostr.Event, admin nostr.PubKey) (Seed, error) {
	var seed Seed
	if event.Kind != 30302 || event.PubKey != admin || !event.CheckID() || !event.VerifySignature() {
		return seed, errors.New("authorization must be a valid super-admin-signed kind 30302")
	}
	var grant LegacyGrant
	if err := json.Unmarshal([]byte(event.Content), &grant); err != nil {
		return seed, errors.New("invalid authorization JSON")
	}
	tags := 0
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "expiration" {
			return seed, errors.New("expiring authorizations cannot migrate")
		}
		if len(tag) > 0 && tag[0] == "d" {
			tags++
			if len(tag) != 2 || tag[1] != grant.CityID {
				return seed, errors.New("city id does not match d tag")
			}
		}
	}
	if tags != 1 || !uuid.MatchString(grant.CityID) || grant.SuperAdminPubkey != admin.Hex() {
		return seed, errors.New("invalid city identifier or super-admin")
	}
	creator, err := nostr.PubKeyFromHex(grant.CreatorPubkey)
	if err != nil {
		return seed, errors.New("invalid creator key")
	}
	if _, err := nostr.IDFromHex(grant.CreatorRevisionID); err != nil {
		return seed, errors.New("invalid original revision id")
	}
	if len(grant.EditorPubkeys) < 1 || len(grant.EditorPubkeys) > 100 {
		return seed, errors.New("invalid editor count")
	}
	editors := make([]nostr.PubKey, 0, len(grant.EditorPubkeys))
	for _, hex := range grant.EditorPubkeys {
		key, err := nostr.PubKeyFromHex(hex)
		if err != nil {
			return seed, errors.New("invalid editor key")
		}
		if !slices.Contains(editors, key) {
			editors = append(editors, key)
		}
	}
	if !slices.Contains(editors, creator) {
		return seed, errors.New("creator missing from editor list")
	}
	slices.SortFunc(editors, func(a, b nostr.PubKey) int {
		if a.Hex() < b.Hex() {
			return -1
		}
		if a == b {
			return 0
		}
		return 1
	})
	return Seed{CityID: grant.CityID, Creator: creator, CreatorRevisionID: grant.CreatorRevisionID, SourceID: event.ID, SourceTimestamp: event.CreatedAt, Admin: admin, Editors: editors}, nil
}

// SelectSeeds resolves addressable authorizations using NIP-01 ordering, rather
// than unioning historical editor lists (which would resurrect revoked editors).
// Any malformed eligible admin record fails the audit rather than disappearing.
func SelectSeeds(events []nostr.Event, admin nostr.PubKey) ([]Seed, error) {
	newest := map[string]nostr.Event{}
	for _, event := range events {
		if event.Kind != 30302 || event.PubKey != admin {
			continue
		}
		seed, err := SeedFromGrant(event, admin)
		if err != nil {
			return nil, fmt.Errorf("authorization %s: %w", event.ID.Hex(), err)
		}
		prior, exists := newest[seed.CityID]
		if !exists || nostr.IsOlder(prior, event) {
			newest[seed.CityID] = event
		}
	}
	result := make([]Seed, 0, len(newest))
	for _, event := range newest {
		seed, _ := SeedFromGrant(event, admin)
		result = append(result, seed)
	}
	slices.SortFunc(result, func(a, b Seed) int {
		if a.CityID < b.CityID {
			return -1
		}
		if a.CityID > b.CityID {
			return 1
		}
		return 0
	})
	return result, nil
}

// Group exposes current editors as scoped group roles. Group membership is not
// BitcoinWalk approval. No chat, joining, private groups or calendar publication
// is advertised by this initial metadata model.
func (s Seed) Group(relayURL string, relayKey nostr.PubKey, now nostr.Timestamp) nip29.Group {
	group := nip29.Group{
		Address: nip29.GroupAddress{Relay: relayURL, ID: s.CityID, Self: relayKey},
		Name:    "BitcoinWalk city " + s.CityID,
		About:   "BitcoinWalk organizer group. Group membership does not imply an approved walk.",
		Members: map[nostr.PubKey][]*nip29.Role{}, Closed: true, Restricted: true,
		SupportedKinds:     []nostr.Kind{30303, 30304},
		Roles:              []*nip29.Role{{Name: SuperAdmin, Description: "Manage group roles and approve BitcoinWalk revisions across cities"}, {Name: Owner, Description: "City creator; submit revisions for this city"}, {Name: Organizer, Description: "Submit revisions for this city; cannot approve or manage roles"}},
		LastMetadataUpdate: now, LastAdminsUpdate: now, LastMembersUpdate: now, LastRolesUpdate: now,
	}
	for _, key := range s.Editors {
		group.Members[key] = []*nip29.Role{{Name: Organizer}}
	}
	group.Members[s.Creator] = []*nip29.Role{{Name: Owner}}
	group.Members[s.Admin] = append(group.Members[s.Admin], &nip29.Role{Name: SuperAdmin})
	return group
}

// Metadata uses only the dedicated relay key; caller owns secure storage and
// deployment. Generated events are returned, never published by this package.
func (s Seed) Metadata(relayURL string, relaySecret nostr.SecretKey, now nostr.Timestamp) ([]nostr.Event, error) {
	group := s.Group(relayURL, nostr.GetPublicKey(relaySecret), now)
	if group.Address.Self == s.Admin || slices.Contains(s.Editors, group.Address.Self) {
		return nil, errors.New("relay identity must be separate from all user identities")
	}
	events := []nostr.Event{group.ToMetadataEvent(), group.ToAdminsEvent(), group.ToMembersEvent(), group.ToRolesEvent()}
	for i := range events {
		// Upstream uses map iteration for members. Sort to produce stable IDs on rebuild.
		sort.SliceStable(events[i].Tags, func(a, b int) bool {
			left, _ := json.Marshal(events[i].Tags[a])
			right, _ := json.Marshal(events[i].Tags[b])
			return string(left) < string(right)
		})
		if err := events[i].Sign(relaySecret); err != nil {
			return nil, err
		}
	}
	return events, nil
}
