package main

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
)

var managedWalkImagePattern = regexp.MustCompile(`^https://(?:app-staging\.)?bitcoinwalk\.org/api/media/files/[0-9a-f]{64}\.webp$`)
var allTrailsRoutePattern = regexp.MustCompile(`^https://www\.alltrails\.com/explore/trail/[A-Za-z0-9._~!$&'()*+,;=:@%/-]+(?:\?[A-Za-z0-9._~!$&'()*+,;=:@%/?-]*)?$`)

const geohashAlphabet = "0123456789bcdefghjkmnpqrstuvwxyz"

func encodeGeohash(latitude, longitude float64) string {
	latRange, lonRange := [2]float64{-90, 90}, [2]float64{-180, 180}
	even, value, bits, result := true, 0, 0, make([]byte, 0, 9)
	for len(result) < 9 {
		rangeValue, coordinate := &latRange, latitude
		if even {
			rangeValue, coordinate = &lonRange, longitude
		}
		midpoint := (rangeValue[0] + rangeValue[1]) / 2
		value <<= 1
		if coordinate >= midpoint {
			value++
			rangeValue[0] = midpoint
		} else {
			rangeValue[1] = midpoint
		}
		even = !even
		bits++
		if bits == 5 {
			result = append(result, geohashAlphabet[value])
			value, bits = 0, 0
		}
	}
	return string(result)
}

func validateOptionalGeohash(event nostr.Event, latitude, longitude float64) error {
	value, count := "", 0
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "g" {
			count++
			if len(tag) != 2 {
				return errors.New("invalid: calendar geohash")
			}
			value = tag[1]
		}
	}
	if count > 1 || count == 1 && value != encodeGeohash(latitude, longitude) {
		return errors.New("invalid: calendar geohash does not match coordinates")
	}
	return nil
}

func (p *organizerPolicy) guardCalendarReads(relay *khatru.Relay) {
	query := relay.QueryStored
	relay.QueryStored = func(ctx context.Context, filter nostr.Filter) iter.Seq[nostr.Event] {
		return func(yield func(nostr.Event) bool) {
			for event := range query(ctx, filter) {
				if event.Kind == 31923 {
					p.mu.Lock()
					err := p.checkCalendarRead(event)
					p.mu.Unlock()
					if err != nil {
						continue
					}
				}
				if !yield(event) {
					return
				}
			}
		}
	}
	previous := relay.PreventBroadcast
	relay.PreventBroadcast = func(ws *khatru.WebSocket, filter nostr.Filter, event nostr.Event) bool {
		if previous != nil && previous(ws, filter, event) {
			return true
		}
		if event.Kind != 31923 {
			return false
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.checkCalendarRead(event) != nil
	}
}

// Same retained-approval semantics as the public website: a rejected alternate
// draft does not revoke the currently approved walk. A revocation is city-wide.
func (p *organizerPolicy) currentApproval(cityID string) *nostr.Event {
	records := map[nostr.ID]nostr.Event{}
	if old := p.find(30304, &p.admin, cityID); old != nil {
		records[old.ID] = *old
	}
	for e := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30304}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"i": []string{cityID}}}, 10001) {
		records[e.ID] = e
		if len(records) > 10000 {
			return nil
		} // fail closed, never choose from truncated history
	}
	ordered := make([]nostr.Event, 0, len(records))
	for _, e := range records {
		ordered = append(ordered, e)
	}
	slices.SortFunc(ordered, func(a, b nostr.Event) int {
		if a.CreatedAt > b.CreatedAt {
			return -1
		}
		if a.CreatedAt < b.CreatedAt {
			return 1
		}
		return strings.Compare(a.ID.Hex(), b.ID.Hex())
	})
	rejected := map[string]bool{}
	for _, e := range ordered {
		var decision cityDecision
		if json.Unmarshal([]byte(e.Content), &decision) != nil || decision.CityID != cityID {
			continue
		}
		if decision.Status == "rejected" {
			rejected[decision.RevisionID] = true
			continue
		}
		if decision.Status == "approved" && !rejected[decision.RevisionID] {
			return &e
		}
		return nil
	}
	return nil
}

func calendarReference(event nostr.Event, marker string) (string, error) {
	result := ""
	count := 0
	for _, tag := range event.Tags {
		if len(tag) == 4 && tag[0] == "e" && tag[2] == "" && tag[3] == marker {
			result = tag[1]
			count++
		}
	}
	if count != 1 {
		return "", errors.New("invalid: one exact calendar source reference required")
	}
	return result, nil
}

func (p *organizerPolicy) checkCalendar(event nostr.Event) error {
	if err := p.checkPublishingSuspension(event); err != nil {
		return err
	}
	if value, ok := exactCalendarTag(event, "bitcoinwalk"); ok && value == "initial-proposal-v1" {
		return p.validateInitialProposal(event, nil, true)
	}
	if value, ok := exactCalendarTag(event, "bitcoinwalk"); ok && value == "occurrence-v1" {
		return p.checkOrganizerCalendar(event, true)
	}
	return p.checkLegacyCalendar(event)
}

func (p *organizerPolicy) checkCalendarRead(event nostr.Event) error {
	if err := p.checkEventVisibility(event); err != nil {
		return err
	}
	if value, ok := exactCalendarTag(event, "bitcoinwalk"); ok && value == "initial-proposal-v1" {
		cityID := mustCalendarCity(event)
		if p.currentApproval(cityID) == nil {
			return errors.New("restricted: initial walk is awaiting approval")
		}
		var revision *nostr.Event
		seen := 0
		for approval := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30304}, Authors: []nostr.PubKey{p.admin}}, 10001) {
			seen++
			if seen > 10000 {
				return errors.New("restricted: city approval history exceeds the safe read limit")
			}
			var decision cityDecision
			if json.Unmarshal([]byte(approval.Content), &decision) != nil || decision.CityID != cityID || decision.Status != "approved" || decision.InitialEventID != event.ID.Hex() {
				continue
			}
			candidate := p.byID(decision.RevisionID)
			if candidate != nil && candidate.Kind == 30303 && candidate.PubKey == event.PubKey {
				revision = candidate
				break
			}
		}
		if revision == nil {
			return errors.New("restricted: initial walk was not released by approval")
		}
		city, err := parseDraft(*revision)
		if err != nil {
			return err
		}
		return p.validateInitialProposal(event, &city, false)
	}
	if value, ok := exactCalendarTag(event, "bitcoinwalk"); ok && value == "occurrence-v1" {
		return p.checkOrganizerCalendar(event, false)
	}
	return p.checkLegacyCalendar(event)
}

func mustCalendarCity(event nostr.Event) string {
	value, _ := uniqueTag(event, "i")
	return value
}

// Initial proposals are stored before city approval but suppressed from reads
// and broadcasts. Approval releases only the exact organizer-signed event.
func (p *organizerPolicy) validateInitialProposal(event nostr.Event, city *cityDraft, enforceHorizon bool) error {
	cityID, err := uniqueTag(event, "i")
	if err != nil || !uuidPattern.MatchString(cityID) {
		return errors.New("invalid: initial walk city identifier")
	}
	d, err := uniqueTag(event, "d")
	if err != nil || !occurrenceAddressPattern.MatchString(d) || !strings.HasPrefix(d, cityID+":") {
		return errors.New("invalid: initial walk address")
	}
	startText, startOK := exactCalendarTag(event, "start")
	endText, endOK := exactCalendarTag(event, "end")
	start, e1 := strconv.ParseInt(startText, 10, 64)
	end, e2 := strconv.ParseInt(endText, 10, 64)
	if !startOK || !endOK || e1 != nil || e2 != nil || end-start != 3600 || enforceHorizon && (start < time.Now().Add(-5*time.Minute).Unix() || start > time.Now().Add(200*24*time.Hour).Unix()) {
		return errors.New("restricted: initial walk must be a one-hour future event within the publication horizon")
	}
	zone, zoneOK := exactCalendarTag(event, "start_tzid")
	endZone, endZoneOK := exactCalendarTag(event, "end_tzid")
	if !zoneOK || !endZoneOK || zone != endZone || len(zone) > 100 {
		return errors.New("invalid: initial walk timezone")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return errors.New("invalid: initial walk timezone")
	}
	expected := map[string]string{"d": d, "i": cityID, "bitcoinwalk": "initial-proposal-v1", "start": startText, "end": endText, "D": strconv.FormatInt(start/86400, 10), "start_tzid": zone, "end_tzid": zone, "t": "bitcoinwalk"}
	if city != nil {
		expected["title"] = "BitcoinWalk " + city.CityName
		expected["summary"] = "BitcoinWalk in " + city.CityName
		if city.HeroImageURL != "" {
			expected["image"] = city.HeroImageURL
		}
		cityStart, _ := time.Parse(time.RFC3339, city.StartAt)
		if city.CityID != cityID || cityStart.Unix() != start || event.Content != city.Description {
			return errors.New("restricted: initial walk differs from city submission")
		}
	} else {
		for _, name := range []string{"title", "summary"} {
			value, ok := exactCalendarTag(event, name)
			if !ok {
				return errors.New("invalid: initial walk fields")
			}
			expected[name] = value
		}
		imageCount := 0
		for _, tag := range event.Tags {
			if len(tag) > 0 && tag[0] == "image" {
				imageCount++
				if len(tag) != 2 || !webURL(tag[1]) {
					return errors.New("invalid: initial walk fields")
				}
				expected["image"] = tag[1]
			}
		}
		if imageCount > 1 || !strings.HasPrefix(expected["title"], "BitcoinWalk ") || !strings.HasPrefix(expected["summary"], "BitcoinWalk in ") || !sizeOK(event.Content, 1, 5000) {
			return errors.New("invalid: initial walk fields")
		}
	}
	locations, links, eCount := []string{}, []string{}, 0
	allowed := map[string]bool{}
	for name := range expected {
		allowed[name] = true
	}
	allowed["g"] = true
	routeMarker, hasRouteMarker := exactCalendarTag(event, "bitcoinwalk-route")
	if hasRouteMarker {
		if routeMarker != "alltrails-v1" {
			return errors.New("invalid: initial walk route")
		}
		allowed["bitcoinwalk-route"] = true
	}
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			return errors.New("invalid: malformed initial walk tag")
		}
		switch tag[0] {
		case "location":
			if len(tag) != 2 {
				return errors.New("invalid: initial walk location")
			}
			locations = append(locations, tag[1])
		case "r":
			if len(tag) != 2 {
				return errors.New("invalid: initial walk link")
			}
			links = append(links, tag[1])
		case "e":
			eCount++
		default:
			if !allowed[tag[0]] || len(tag) != 2 {
				return errors.New("restricted: unsupported initial walk field")
			}
		}
	}
	if eCount != 0 || len(locations) != 2 || !sizeOK(locations[0], 1, 500) {
		return errors.New("invalid: initial walk location required")
	}
	coords := strings.Split(locations[1], ",")
	if len(coords) != 2 {
		return errors.New("invalid: initial walk coordinates")
	}
	lat, x1 := strconv.ParseFloat(coords[0], 64)
	lon, x2 := strconv.ParseFloat(coords[1], 64)
	if x1 != nil || x2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return errors.New("invalid: initial walk coordinates")
	}
	if err := validateOptionalGeohash(event, lat, lon); err != nil {
		return err
	}
	if city != nil {
		if locations[0] != city.MeetingPoint.Description || lat != *city.MeetingPoint.Latitude || lon != *city.MeetingPoint.Longitude {
			return errors.New("restricted: initial walk location differs from city submission")
		}
		expectedLinks := 0
		if city.ChatURL != "" {
			expectedLinks++
			if !slices.Contains(links, city.ChatURL) {
				return errors.New("restricted: initial walk link differs from city submission")
			}
		}
		if hasRouteMarker {
			expectedLinks++
			found := false
			for _, link := range links {
				if allTrailsRoutePattern.MatchString(link) {
					found = true
				}
			}
			if !found {
				return errors.New("invalid: initial walk route")
			}
		}
		if len(links) != expectedLinks {
			return errors.New("restricted: unexpected initial walk link")
		}
	} else {
		for _, link := range links {
			if !webURL(link) {
				return errors.New("invalid: initial walk link")
			}
		}
	}
	if old := p.find(31923, &event.PubKey, d); old != nil && old.ID != event.ID {
		return errors.New("restricted: initial walk address already submitted")
	}
	return nil
}

func (p *organizerPolicy) checkLegacyCalendar(event nostr.Event) error {
	if p.calendarDeleted(event.ID.Hex(), event.PubKey) {
		return errors.New("restricted: calendar event was deleted; publish a new event")
	}
	if event.PubKey != p.admin {
		return errors.New("restricted: only the super-admin publishes canonical calendar events")
	}
	cityID, err := uniqueTag(event, "i")
	if err != nil || !uuidPattern.MatchString(cityID) {
		return errors.New("invalid: calendar city identifier")
	}
	revisionID, err := calendarReference(event, "city-revision")
	if err != nil {
		return err
	}
	approvalID, err := calendarReference(event, "city-approval")
	if err != nil {
		return err
	}
	approval := p.currentApproval(cityID)
	if approval == nil || approval.ID.Hex() != approvalID {
		return errors.New("restricted: calendar must reference the current city approval")
	}
	var decision cityDecision
	if json.Unmarshal([]byte(approval.Content), &decision) != nil || decision.RevisionID != revisionID {
		return errors.New("restricted: calendar revision is not the approved revision")
	}
	revision := p.byID(revisionID)
	if revision == nil || revision.Kind != 30303 {
		return errors.New("invalid: approved revision unavailable")
	}
	city, err := parseDraft(*revision)
	if err != nil || city.CityID != cityID {
		return errors.New("invalid: calendar city/revision mismatch")
	}
	start, _ := time.Parse(time.RFC3339, city.StartAt)
	expected := map[string]string{"d": cityID, "i": cityID, "title": "BitcoinWalk " + city.CityName, "summary": "BitcoinWalk in " + city.CityName, "start": strconv.FormatInt(start.Unix(), 10), "D": strconv.FormatInt(start.Unix()/86400, 10), "t": "bitcoinwalk"}
	if city.HeroImageURL != "" {
		expected["image"] = city.HeroImageURL
	}
	for name, value := range expected {
		actual, err := uniqueTag(event, name)
		if err != nil || actual != value {
			return errors.New("restricted: calendar fields must exactly match the approved city")
		}
	}
	if event.Content != city.Description {
		return errors.New("restricted: calendar description differs from approved revision")
	}
	locations := []string{}
	references := []string{}
	eCount := 0
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			return errors.New("invalid: malformed calendar tag")
		}
		switch tag[0] {
		case "e":
			eCount++
			if len(tag) != 4 || (tag[3] != "city-revision" && tag[3] != "city-approval") {
				return errors.New("invalid: unexpected calendar reference")
			}
		case "location":
			if len(tag) != 2 {
				return errors.New("invalid: location tag")
			}
			locations = append(locations, tag[1])
		case "r":
			if len(tag) != 2 {
				return errors.New("invalid: link tag")
			}
			references = append(references, tag[1])
		case "g":
			if len(tag) != 2 {
				return errors.New("invalid: calendar geohash")
			}
		default:
			if _, ok := expected[tag[0]]; !ok || len(tag) != 2 {
				return errors.New("restricted: unsupported calendar field")
			}
		}
	}
	if eCount != 2 || len(locations) != 2 || locations[0] != city.MeetingPoint.Description {
		return errors.New("restricted: calendar locations differ from approval")
	}
	coords := strings.Split(locations[1], ",")
	if len(coords) != 2 {
		return errors.New("invalid: calendar coordinates")
	}
	lat, e1 := strconv.ParseFloat(coords[0], 64)
	lon, e2 := strconv.ParseFloat(coords[1], 64)
	if e1 != nil || e2 != nil || lat != *city.MeetingPoint.Latitude || lon != *city.MeetingPoint.Longitude {
		return errors.New("restricted: calendar coordinates differ from approval")
	}
	if err := validateOptionalGeohash(event, lat, lon); err != nil {
		return err
	}
	if city.ChatURL != "" {
		if len(references) != 1 || references[0] != city.ChatURL {
			return errors.New("restricted: calendar link differs from approval")
		}
	} else if len(references) != 0 {
		return errors.New("restricted: unexpected calendar link")
	}
	if old := p.find(31923, &p.admin, cityID); old != nil && old.ID != event.ID && event.CreatedAt <= old.CreatedAt {
		return errors.New("invalid: calendar update must be newer than the existing event")
	}
	return nil
}

var occurrenceAddressPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}:20\d{2}-\d{2}-\d{2}$`)

func exactCalendarTag(event nostr.Event, name string) (string, bool) {
	value, count := "", 0
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == name {
			if len(tag) != 2 {
				return "", false
			}
			value, count = tag[1], count+1
		}
	}
	return value, count == 1
}

// Organizer occurrences are immutable public statements by the organizer. A
// current grant is required when writing, but later editor removal does not
// erase already accepted walks. City-wide revocation and event tombstones do.
func (p *organizerPolicy) checkOrganizerCalendar(event nostr.Event, writing bool) error {
	if p.calendarDeleted(event.ID.Hex(), event.PubKey) {
		return errors.New("restricted: calendar event was deleted; publish a new event")
	}
	cityID, err := uniqueTag(event, "i")
	if err != nil || !uuidPattern.MatchString(cityID) {
		return errors.New("invalid: calendar city identifier")
	}
	d, err := uniqueTag(event, "d")
	if err != nil || !occurrenceAddressPattern.MatchString(d) {
		return errors.New("invalid: occurrence address must contain series and local date")
	}
	revisionID, err := calendarReference(event, "city-revision")
	if err != nil {
		return err
	}
	approvalID, err := calendarReference(event, "city-approval")
	if err != nil {
		return err
	}
	revision := p.byID(revisionID)
	approval := p.byID(approvalID)
	if revision == nil || revision.Kind != 30303 || approval == nil || approval.Kind != 30304 || approval.PubKey != p.admin {
		return errors.New("invalid: approved occurrence source unavailable")
	}
	city, err := parseDraft(*revision)
	if err != nil || city.CityID != cityID {
		return errors.New("invalid: occurrence city/revision mismatch")
	}
	var decision cityDecision
	if json.Unmarshal([]byte(approval.Content), &decision) != nil || decision.CityID != cityID || decision.RevisionID != revisionID || decision.Status != "approved" {
		return errors.New("invalid: occurrence approval mismatch")
	}
	current := p.currentApproval(cityID)
	if current == nil {
		return errors.New("restricted: city is not currently approved")
	}
	if writing {
		if current.ID.Hex() != approvalID {
			return errors.New("restricted: occurrence must reference the current city approval")
		}
		grant, _, grantErr := p.grant(cityID)
		if grantErr != nil {
			return grantErr
		}
		if !p.canEdit(event.PubKey, grant) {
			return errors.New("restricted: you are not an editor of this city")
		}
	}
	expected := map[string]string{
		"d": d, "i": cityID, "bitcoinwalk": "occurrence-v1",
		"title": "BitcoinWalk " + city.CityName, "summary": "BitcoinWalk in " + city.CityName,
		"t": "bitcoinwalk",
	}
	imageMarker, hasImageMarker := exactCalendarTag(event, "bitcoinwalk-image")
	image, hasImage := exactCalendarTag(event, "image")
	if hasImageMarker {
		if imageMarker != "override-v1" || !hasImage || !managedWalkImagePattern.MatchString(image) {
			return errors.New("invalid: occurrence image override")
		}
		expected["image"] = image
		expected["bitcoinwalk-image"] = imageMarker
	} else if city.HeroImageURL != "" {
		expected["image"] = city.HeroImageURL
	} else if hasImage {
		return errors.New("restricted: occurrence image differs from approved city")
	}
	for name, value := range expected {
		actual, ok := exactCalendarTag(event, name)
		if !ok || actual != value {
			return errors.New("restricted: occurrence fields differ from approved city")
		}
	}
	if !sizeOK(strings.TrimSpace(event.Content), 1, 5000) {
		return errors.New("invalid: occurrence description")
	}
	startText, ok := exactCalendarTag(event, "start")
	if !ok {
		return errors.New("invalid: occurrence start")
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start <= 0 {
		return errors.New("invalid: occurrence start")
	}
	endText, ok := exactCalendarTag(event, "end")
	if !ok {
		return errors.New("invalid: occurrence end")
	}
	end, err := strconv.ParseInt(endText, 10, 64)
	if err != nil || end-start < 900 || end-start > 43200 {
		return errors.New("invalid: occurrence duration")
	}
	dayText, ok := exactCalendarTag(event, "D")
	if !ok || dayText != strconv.FormatInt(start/86400, 10) {
		return errors.New("invalid: occurrence day")
	}
	zone, ok := exactCalendarTag(event, "start_tzid")
	if !ok || len(zone) > 100 {
		return errors.New("invalid: occurrence timezone")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return errors.New("invalid: occurrence timezone")
	}
	endZone, ok := exactCalendarTag(event, "end_tzid")
	if !ok || endZone != zone {
		return errors.New("invalid: occurrence end timezone")
	}
	if writing && (start < time.Now().Add(-5*time.Minute).Unix() || start > time.Now().Add(200*24*time.Hour).Unix()) {
		return errors.New("restricted: occurrence must be within the publication horizon")
	}
	locations, references, eCount := []string{}, []string{}, 0
	allowed := map[string]bool{"d": true, "i": true, "bitcoinwalk": true, "title": true, "summary": true, "start": true, "end": true, "D": true, "start_tzid": true, "end_tzid": true, "t": true, "g": true}
	if city.HeroImageURL != "" || hasImageMarker {
		allowed["image"] = true
	}
	if hasImageMarker {
		allowed["bitcoinwalk-image"] = true
	}
	routeMarker, hasRouteMarker := exactCalendarTag(event, "bitcoinwalk-route")
	if hasRouteMarker {
		if routeMarker != "alltrails-v1" {
			return errors.New("invalid: occurrence route")
		}
		allowed["bitcoinwalk-route"] = true
	}
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			return errors.New("invalid: malformed occurrence tag")
		}
		switch tag[0] {
		case "e":
			eCount++
			if len(tag) != 4 || (tag[3] != "city-revision" && tag[3] != "city-approval") {
				return errors.New("invalid: unexpected occurrence reference")
			}
		case "location":
			if len(tag) != 2 {
				return errors.New("invalid: occurrence location")
			}
			locations = append(locations, tag[1])
		case "r":
			if len(tag) != 2 {
				return errors.New("invalid: occurrence link")
			}
			references = append(references, tag[1])
		default:
			if !allowed[tag[0]] || len(tag) != 2 {
				return errors.New("restricted: unsupported occurrence field")
			}
		}
	}
	if eCount != 2 || len(locations) != 2 || !sizeOK(locations[0], 1, 500) {
		return errors.New("invalid: occurrence location required")
	}
	coords := strings.Split(locations[1], ",")
	if len(coords) != 2 {
		return errors.New("invalid: occurrence coordinates")
	}
	lat, e1 := strconv.ParseFloat(coords[0], 64)
	lon, e2 := strconv.ParseFloat(coords[1], 64)
	if e1 != nil || e2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return errors.New("invalid: occurrence coordinates")
	}
	if err := validateOptionalGeohash(event, lat, lon); err != nil {
		return err
	}
	expectedLinks := 0
	if city.ChatURL != "" {
		expectedLinks++
		if !slices.Contains(references, city.ChatURL) {
			return errors.New("restricted: occurrence link differs from approved city")
		}
	}
	if hasRouteMarker {
		expectedLinks++
		found := false
		for _, link := range references {
			if allTrailsRoutePattern.MatchString(link) {
				found = true
			}
		}
		if !found {
			return errors.New("invalid: occurrence route")
		}
	}
	if len(references) != expectedLinks {
		return errors.New("restricted: unexpected occurrence link")
	}
	if old := p.find(31923, &event.PubKey, d); old != nil && old.ID != event.ID && event.CreatedAt <= old.CreatedAt {
		return errors.New("invalid: occurrence update must be newer than the existing event")
	}
	return nil
}

func (p *organizerPolicy) calendarDeleted(id string, author nostr.PubKey) bool {
	authors := []nostr.PubKey{p.admin}
	if author != p.admin {
		authors = append(authors, author)
	}
	for _, signer := range authors {
		for range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{5}, Authors: []nostr.PubKey{signer}, Tags: nostr.TagMap{"e": []string{id}}}, 1) {
			return true
		}
	}
	return false
}

func (p *organizerPolicy) checkCalendarDeletion(event nostr.Event) error {
	id, err := uniqueTag(event, "e")
	if err != nil {
		return errors.New("invalid: one exact event ID required")
	}
	kind, err := uniqueTag(event, "k")
	if err != nil || kind != "31923" {
		return errors.New("restricted: only calendar deletion supported")
	}
	cityID, err := uniqueTag(event, "i")
	if err != nil || !uuidPattern.MatchString(cityID) || len(event.Tags) != 3 || !sizeOK(event.Content, 1, 500) {
		return errors.New("invalid: deletion scope or warning")
	}
	for _, tag := range event.Tags {
		if len(tag) != 2 {
			return errors.New("invalid: deletion tag")
		}
	}
	target := p.byID(id)
	if target == nil || target.Kind != 31923 {
		return errors.New("restricted: target must be a stored calendar event")
	}
	if event.PubKey != p.admin && event.PubKey != target.PubKey {
		return errors.New("restricted: only the event author or super-admin may cancel this walk")
	}
	targetCity, err := uniqueTag(*target, "i")
	if err != nil || targetCity != cityID || event.CreatedAt < target.CreatedAt {
		return errors.New("invalid: deletion city or timestamp mismatch")
	}
	return nil
}
