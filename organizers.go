package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/khatru/policies"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Legacy addresses remain readable. New writes use a distinct address for each
// revision/decision; the storage guard below prevents address reuse.
func workflowAddress(d, cityID string) bool {
	return d == cityID || strings.HasPrefix(d, cityID+":") && uuidPattern.MatchString(strings.TrimPrefix(d, cityID+":"))
}

type cityDraft struct {
	CityID       string   `json:"cityId"`
	Slug         string   `json:"slug"`
	CityName     string   `json:"cityName"`
	Aliases      []string `json:"aliases,omitempty"`
	StartAt      string   `json:"startAt"`
	Description  string   `json:"description"`
	MeetingPoint struct {
		Description string   `json:"description"`
		Latitude    *float64 `json:"latitude"`
		Longitude   *float64 `json:"longitude"`
	} `json:"meetingPoint"`
	ChatURL       string `json:"chatUrl"`
	HeroImageURL  string `json:"heroImageUrl"`
	RequestedTier string `json:"requestedTier,omitempty"`
}

// The two creator fields extend the web app's not-yet-published authorization schema.
// The original submitted event proves which public key created this city.
type cityGrant struct {
	CityID            string   `json:"cityId"`
	EditorPubkeys     []string `json:"editorPubkeys"`
	SuperAdminPubkey  string   `json:"superAdminPubkey"`
	CreatorPubkey     string   `json:"creatorPubkey"`
	CreatorRevisionID string   `json:"creatorRevisionId"`
}
type cityDecision struct {
	CityID         string `json:"cityId"`
	RevisionID     string `json:"cityRevisionId"`
	InitialEventID string `json:"initialEventId,omitempty"`
	HeroImageURL   string `json:"heroImageUrl,omitempty"`
	Slug           string `json:"slug,omitempty"`
	Status         string `json:"status"`
	Note           string `json:"note"`
}

type organizerPolicy struct {
	db             *boltdb.BoltBackend
	admin          nostr.PubKey
	mu             sync.Mutex
	limits         *chatLimits
	replicaJournal *replicaJournal
}

func uniqueTag(event nostr.Event, name string) (string, error) {
	var values []string
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == name {
			if len(tag) < 2 {
				return "", fmt.Errorf("invalid: malformed %s tag", name)
			}
			values = append(values, tag[1])
		}
	}
	if len(values) != 1 || values[0] == "" {
		return "", fmt.Errorf("invalid: exactly one %s tag required", name)
	}
	return values[0], nil
}
func sizeOK(s string, min, max int) bool { n := utf8.RuneCountInString(s); return n >= min && n <= max }
func webURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil
}
func validCityAliases(city cityDraft) bool {
	if len(city.Aliases) > 20 {
		return false
	}
	seen := make(map[string]bool, len(city.Aliases))
	canonical := strings.ToLower(strings.TrimSpace(city.CityName))
	for _, alias := range city.Aliases {
		trimmed := strings.TrimSpace(alias)
		key := strings.ToLower(trimmed)
		if trimmed != alias || !sizeOK(alias, 1, 100) || key == canonical || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
func parseDraft(event nostr.Event) (cityDraft, error) {
	var city cityDraft
	if json.Unmarshal([]byte(event.Content), &city) != nil {
		return city, errors.New("invalid: city JSON required")
	}
	d, err := uniqueTag(event, "d")
	slug, slugErr := uniqueTag(event, "city")
	_, dateErr := time.Parse(time.RFC3339, city.StartAt)
	if err != nil || slugErr != nil || !uuidPattern.MatchString(city.CityID) || !workflowAddress(d, city.CityID) || slug != city.Slug || !slugPattern.MatchString(city.Slug) || !sizeOK(city.Slug, 2, 63) || !sizeOK(city.CityName, 1, 100) || !validCityAliases(city) || !sizeOK(city.Description, 1, 5000) || !sizeOK(city.MeetingPoint.Description, 1, 500) || dateErr != nil || !webURL(city.ChatURL) || city.HeroImageURL != "" && !webURL(city.HeroImageURL) {
		return city, errors.New("invalid: city document or city tags do not match the required schema")
	}
	lat, lon := city.MeetingPoint.Latitude, city.MeetingPoint.Longitude
	if lat == nil || lon == nil || *lat < -90 || *lat > 90 || *lon < -180 || *lon > 180 {
		return city, errors.New("invalid: meeting point coordinates required")
	}
	return city, nil
}

func (p *organizerPolicy) find(kind nostr.Kind, author *nostr.PubKey, d string) *nostr.Event {
	filter := nostr.Filter{Kinds: []nostr.Kind{kind}, Tags: nostr.TagMap{"d": []string{d}}}
	if author != nil {
		filter.Authors = []nostr.PubKey{*author}
	}
	for event := range p.db.QueryEvents(filter, 1) {
		return &event
	}
	return nil
}
func (p *organizerPolicy) byID(id string) *nostr.Event {
	parsed, err := nostr.IDFromHex(id)
	if err != nil {
		return nil
	}
	for event := range p.db.QueryEvents(nostr.Filter{IDs: []nostr.ID{parsed}}, 1) {
		return &event
	}
	return nil
}
func (p *organizerPolicy) grant(cityID string) (*cityGrant, *nostr.Event, error) {
	event := p.find(30302, &p.admin, cityID)
	if event == nil {
		return nil, nil, nil
	}
	var grant cityGrant
	if json.Unmarshal([]byte(event.Content), &grant) != nil || grant.CityID != cityID || grant.SuperAdminPubkey != p.admin.Hex() || grant.CreatorPubkey == "" {
		return nil, nil, errors.New("restricted: invalid stored authorization; admin repair required")
	}
	return &grant, event, nil
}
func (p *organizerPolicy) canEdit(key nostr.PubKey, grant *cityGrant) bool {
	return key == p.admin || grant != nil && (key.Hex() == grant.CreatorPubkey || slices.Contains(grant.EditorPubkeys, key.Hex()))
}

func (p *organizerPolicy) latestDecision(cityID string) *nostr.Event {
	latest := p.find(30304, &p.admin, cityID) // pre-upgrade decision
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30304}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"i": []string{cityID}}}, 1) {
		if latest == nil || event.CreatedAt > latest.CreatedAt {
			latest = &event
		}
	}
	return latest
}

func (p *organizerPolicy) revisionDecided(id string) bool {
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30304}, Authors: []nostr.PubKey{p.admin}}, 10001) {
		var decision cityDecision
		if json.Unmarshal([]byte(event.Content), &decision) == nil && decision.RevisionID == id {
			return true
		}
	}
	return false
}

// Only an exact retained release permits restoring a past initial walk.
// Revocations do not erase that provenance; new submissions still need a future date.
func (p *organizerPolicy) previouslyReleasedInitial(cityID, revisionID, initialID string) bool {
	seen := 0
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30304}, Authors: []nostr.PubKey{p.admin}}, 10001) {
		seen++
		if seen > 10000 {
			return false
		}
		var decision cityDecision
		if json.Unmarshal([]byte(event.Content), &decision) == nil && decision.Status == "approved" && decision.CityID == cityID && decision.RevisionID == revisionID && decision.InitialEventID == initialID && event.CheckID() && event.VerifySignature() {
			return true
		}
	}
	return false
}

func (p *organizerPolicy) hasOtherPendingCity(author nostr.PubKey, cityID string) bool {
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30303}, Authors: []nostr.PubKey{author}}, 501) {
		city, err := parseDraft(event)
		if err == nil && city.CityID != cityID && !p.revisionDecided(event.ID.Hex()) {
			return true
		}
	}
	return false
}

func (p *organizerPolicy) check(ctx context.Context, event nostr.Event) error {
	if !khatru.IsAuthed(ctx, event.PubKey) {
		return errors.New("auth-required: authenticate as the event author")
	}
	if event.CreatedAt > nostr.Now()+60 {
		return errors.New("invalid: event is too far in the future")
	}
	// Authority must not vanish through expiration or deletion. Draft expiry could
	// also break creator/approval references, so all workflow events are durable.
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "expiration" {
			return errors.New("restricted: workflow events cannot expire")
		}
	}
	if event.Kind == 30303 || event.Kind == 30304 || event.Kind == contentPageKind || event.Kind == featureFlagsKind {
		d, err := uniqueTag(event, "d")
		if err != nil {
			return err
		}
		if old := p.find(event.Kind, &event.PubKey, d); old != nil && old.ID != event.ID {
			return errors.New("restricted: revision and decision addresses are immutable; use a new revision address")
		}
	}
	switch event.Kind {
	case contentPageKind:
		return p.checkContent(event)
	case featureFlagsKind:
		return p.checkFeatureFlags(event)
	case delegationKind:
		return p.checkDelegation(event)
	case delegationAcceptanceKind:
		return p.checkDelegationAcceptance(event)
	case 5:
		return p.checkCalendarDeletion(event)
	case 31923:
		return p.checkCalendar(event)
	case 30303:
		city, err := parseDraft(event)
		if err != nil {
			return err
		}
		grant, _, err := p.grant(city.CityID)
		if err != nil {
			return err
		}
		if grant != nil && !p.canEdit(event.PubKey, grant) {
			return errors.New("restricted: you are not an editor of this city")
		}
		if city.RequestedTier != "" && city.RequestedTier != "free" && city.RequestedTier != "paid" {
			return errors.New("invalid: requested tier")
		}
		if grant == nil && city.RequestedTier == "paid" && !p.paidRegistrationEnabled() {
			return errors.New("restricted: paid registration is not currently enabled")
		}
		if grant == nil && p.hasOtherPendingCity(event.PubKey, city.CityID) {
			return errors.New("rate-limited: this identity already has a city awaiting review")
		}
		initialID, initialCount := "", 0
		for _, tag := range event.Tags {
			if len(tag) == 4 && tag[0] == "e" && tag[2] == "" && tag[3] == "initial-walk" {
				initialID, initialCount = tag[1], initialCount+1
			}
		}
		if initialCount > 0 {
			if initialCount != 1 || grant != nil {
				return errors.New("invalid: initial walk is only allowed on a new city submission")
			}
			proposal := p.byID(initialID)
			if proposal == nil || proposal.Kind != 31923 || proposal.PubKey != event.PubKey {
				return errors.New("invalid: organizer-signed initial walk not found")
			}
			if err := p.validateInitialProposal(*proposal, &city, true); err != nil {
				return err
			}
		}
		// No grant means a pending proposal, not ownership or public approval.
		for _, tag := range event.Tags {
			if len(tag) > 3 && tag[0] == "e" && tag[3] == "previous" {
				previous := p.byID(tag[1])
				if previous == nil || previous.Kind != 30303 {
					return errors.New("invalid: previous revision not found")
				}
				prior, e := parseDraft(*previous)
				if e != nil || prior.CityID != city.CityID || grant == nil && previous.PubKey != event.PubKey && event.PubKey != p.admin {
					return errors.New("restricted: previous revision belongs to another proposal")
				}
			}
		}
		return nil
	case 30302:
		if event.PubKey != p.admin {
			return errors.New("restricted: only the super-admin can change city editors")
		}
		var grant cityGrant
		if json.Unmarshal([]byte(event.Content), &grant) != nil {
			return errors.New("invalid: authorization JSON required")
		}
		d, err := uniqueTag(event, "d")
		if err != nil || !uuidPattern.MatchString(grant.CityID) || d != grant.CityID || grant.SuperAdminPubkey != p.admin.Hex() || len(grant.EditorPubkeys) == 0 || len(grant.EditorPubkeys) > 100 {
			return errors.New("invalid: authorization fields or city identifier")
		}
		if _, err := nostr.PubKeyFromHex(grant.CreatorPubkey); err != nil {
			return errors.New("invalid: creator public key")
		}
		for _, key := range grant.EditorPubkeys {
			if _, err := nostr.PubKeyFromHex(key); err != nil {
				return errors.New("invalid: editor public key")
			}
		}
		if !slices.Contains(grant.EditorPubkeys, grant.CreatorPubkey) {
			return errors.New("restricted: creator must remain a city editor")
		}
		old, oldEvent, err := p.grant(grant.CityID)
		if err != nil {
			return err
		}
		if old != nil {
			if old.CreatorPubkey != grant.CreatorPubkey || old.CreatorRevisionID != grant.CreatorRevisionID {
				return errors.New("restricted: city creator cannot be reassigned")
			}
			if event.ID != oldEvent.ID && event.CreatedAt <= oldEvent.CreatedAt {
				return errors.New("invalid: editor list must be newer than current authorization")
			}
		} else {
			original := p.byID(grant.CreatorRevisionID)
			if original == nil || original.Kind != 30303 || original.PubKey.Hex() != grant.CreatorPubkey {
				return errors.New("invalid: creator must match the referenced submission")
			}
			city, err := parseDraft(*original)
			if err != nil || city.CityID != grant.CityID {
				return errors.New("invalid: creator submission belongs to another city")
			}
		}
		return nil
	case 30304:
		if event.PubKey != p.admin {
			return errors.New("restricted: only the super-admin can approve or revoke walks")
		}
		var decision cityDecision
		if json.Unmarshal([]byte(event.Content), &decision) != nil {
			return errors.New("invalid: approval JSON required")
		}
		d, e := uniqueTag(event, "d")
		status, se := uniqueTag(event, "status")
		if e != nil || se != nil || !workflowAddress(d, decision.CityID) || !uuidPattern.MatchString(decision.CityID) || status != decision.Status || !slices.Contains([]string{"approved", "rejected", "revoked"}, status) || !sizeOK(decision.Note, 0, 500) {
			return errors.New("invalid: approval fields")
		}
		if decision.HeroImageURL != "" && !webURL(decision.HeroImageURL) {
			return errors.New("invalid: approval hero image URL")
		}
		cityTags := 0
		for _, tag := range event.Tags {
			if len(tag) > 0 && tag[0] == "city" {
				cityTags++
				if decision.Slug == "" || len(tag) != 2 || tag[1] != decision.Slug {
					return errors.New("invalid: approval city slug")
				}
			}
		}
		if decision.Slug != "" && (!slugPattern.MatchString(decision.Slug) || !sizeOK(decision.Slug, 2, 63) || cityTags != 1) || decision.Slug == "" && cityTags != 0 {
			return errors.New("invalid: approval city slug")
		}
		if d != decision.CityID {
			cityTag, err := uniqueTag(event, "i")
			if err != nil || cityTag != decision.CityID {
				return errors.New("invalid: decision city index required")
			}
		}
		matched := 0
		initialTag := ""
		initialTags := 0
		for _, tag := range event.Tags {
			if len(tag) > 3 && tag[0] == "e" && tag[3] == "city-revision" && tag[1] == decision.RevisionID {
				matched++
			}
			if len(tag) > 3 && tag[0] == "e" && tag[2] == "" && tag[3] == "initial-walk" {
				initialTag, initialTags = tag[1], initialTags+1
			}
		}
		if matched != 1 {
			return errors.New("invalid: approval must reference the exact city revision")
		}
		if (decision.InitialEventID == "") != (initialTags == 0) || initialTags > 1 || initialTag != decision.InitialEventID {
			return errors.New("invalid: decision initial-walk reference mismatch")
		}
		previous := p.latestDecision(decision.CityID)
		if previous != nil && event.ID != previous.ID && event.CreatedAt <= previous.CreatedAt && p.byID(event.ID.Hex()) == nil {
			return errors.New("invalid: decision must be newer than current decision")
		}
		revision := p.byID(decision.RevisionID)
		if revision == nil || revision.Kind != 30303 {
			// A replaceable revision can disappear, but an existing approval can still be revoked.
			var old cityDecision
			if status == "revoked" && previous != nil && json.Unmarshal([]byte(previous.Content), &old) == nil && old.CityID == decision.CityID && old.RevisionID == decision.RevisionID {
				return nil
			}
			return errors.New("invalid: referenced city revision not found")
		}
		city, err := parseDraft(*revision)
		if err != nil || city.CityID != decision.CityID {
			return errors.New("invalid: approval city does not match revision")
		}
		if status == "approved" {
			revisionInitial, count := "", 0
			for _, tag := range revision.Tags {
				if len(tag) == 4 && tag[0] == "e" && tag[2] == "" && tag[3] == "initial-walk" {
					revisionInitial, count = tag[1], count+1
				}
			}
			if count > 0 {
				if count != 1 || decision.InitialEventID != revisionInitial {
					return errors.New("restricted: approval must release the submitted initial walk")
				}
				proposal := p.byID(revisionInitial)
				if proposal == nil || proposal.Kind != 31923 || proposal.PubKey != revision.PubKey {
					return errors.New("invalid: submitted initial walk unavailable")
				}
				previouslyReleased := p.previouslyReleasedInitial(decision.CityID, decision.RevisionID, revisionInitial)
				if err := p.validateInitialProposal(*proposal, &city, !previouslyReleased); err != nil {
					return err
				}
			}
			grant, _, err := p.grant(decision.CityID)
			if err != nil {
				return err
			}
			if grant == nil || !p.canEdit(revision.PubKey, grant) {
				return errors.New("restricted: establish city editors before approving this author")
			}
		}
		return nil
	default:
		// Canonical calendar/profile publication needs an approved-revision binding.
		// Fail closed until that publishing workflow is implemented.
		return errors.New("restricted: this event kind is not enabled in organizer mode")
	}
}

func enableOrganizers(relay *khatru.Relay, db *boltdb.BoltBackend, admin nostr.PubKey) *organizerPolicy {
	p := &organizerPolicy{db: db, admin: admin, limits: &chatLimits{}}
	relay.OnEvent = policies.SeqEvent(func(ctx context.Context, event nostr.Event) (bool, string) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if err := p.check(ctx, event); err != nil {
			return true, err.Error()
		}
		// Authentication challenges and invalid events must not consume the small
		// new-city budget. Browser clients normally publish once before NIP-42
		// authentication and then retransmit the same signed event. Exact stored
		// retransmissions are idempotent and must not consume another slot either.
		if p.byID(event.ID.Hex()) == nil {
			now := time.Now()
			if !p.limits.allow("organizer-write:"+event.PubKey.Hex(), 120, time.Hour, now) {
				return true, "rate-limited: organizer write limit"
			}
			if value, ok := exactCalendarTag(event, "bitcoinwalk"); ok && value == "initial-proposal-v1" {
				if !p.limits.allow("initial-walk:"+event.PubKey.Hex(), 3, 24*time.Hour, now) {
					return true, "rate-limited: initial walk submission limit"
				}
				if ip := khatru.GetIP(ctx); ip != "" && !p.limits.allow("initial-walk-ip:"+ip, 30, 24*time.Hour, now) {
					return true, "rate-limited: initial walk network limit"
				}
			}
		}
		return false, ""
	}, policies.EventRejectionStrictDefaults)
	// Check again under a lock covering the actual write: a revoked editor cannot
	// pass a pre-check and then race an admin authorization update into storage.
	wrap := func(save func(context.Context, nostr.Event) error) func(context.Context, nostr.Event) error {
		return func(ctx context.Context, event nostr.Event) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			if err := p.check(ctx, event); err != nil {
				return err
			}
			if err := save(ctx, event); err != nil {
				return err
			}
			if p.replicaJournal != nil {
				_, _, err := p.replicaJournal.recordAcceptedLocked(p, event)
				if err != nil {
					p.replicaJournal.setUnhealthy(err)
					return err
				}
			}
			return nil
		}
	}
	relay.StoreEvent = wrap(relay.StoreEvent)
	relay.ReplaceEvent = wrap(relay.ReplaceEvent)
	p.guardCalendarReads(relay)
	// Keep retained workflow history and deletion tombstones. NIP-09 permits
	// stopping publication; the calendar guards suppress deleted IDs permanently.
	relay.DeleteEvent = nil
	relay.Info.Description = env("RELAY_DESCRIPTION", "BitcoinWalk staging: authenticated organizer proposals, admin-managed city editors, public reads.")
	relay.Info.Version = env("RELAY_VERSION", "bitcoinwalk-organizers-0.8.24")
	return p
}
