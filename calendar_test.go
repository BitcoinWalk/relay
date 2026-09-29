package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func TestCalendarApprovalBinding(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	original := draftEvent(t, creator, cityA, 0)
	accept(t, relay, original)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: original.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approve := func(revision nostr.Event, status string, offset int) nostr.Event {
		return workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: status}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", status}}, offset)
	}
	approval := approve(original, "approved", 2)
	calendar := func(revision, decision nostr.Event, offset int) nostr.Event {
		city, e := parseDraft(revision)
		if e != nil {
			t.Fatal(e)
		}
		start, _ := time.Parse(time.RFC3339, city.StartAt)
		e1 := nostr.Event{Kind: 31923, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Content: city.Description, Tags: nostr.Tags{{"d", city.CityID}, {"title", "BitcoinWalk " + city.CityName}, {"summary", "BitcoinWalk in " + city.CityName}, {"image", city.HeroImageURL}, {"start", strconv.FormatInt(start.Unix(), 10)}, {"D", strconv.FormatInt(start.Unix()/86400, 10)}, {"location", city.MeetingPoint.Description}, {"location", "1,2"}, {"t", "bitcoinwalk"}, {"r", city.ChatURL}, {"i", city.CityID}, {"e", revision.ID.Hex(), "", "city-revision"}, {"e", decision.ID.Hex(), "", "city-approval"}}}
		e1.Sign(admin)
		return e1
	}
	e := calendar(original, approval, 3)
	deny(t, relay, e) // approval must already exist
	accept(t, relay, approval)
	bad := e
	bad.Sign(creator)
	deny(t, relay, bad)
	if _, err := relay.AddEvent(t.Context(), e); err == nil {
		t.Fatal("unauthenticated publication accepted")
	}
	for _, mutate := range []func(*nostr.Event){
		func(e *nostr.Event) { e.Content = "unapproved description" },
		func(e *nostr.Event) { e.Tags[4] = nostr.Tag{"start", "99999999"} },
		func(e *nostr.Event) { e.Tags[7] = nostr.Tag{"location", "3,4"} },
		func(e *nostr.Event) { e.Tags = append(e.Tags, nostr.Tag{"end", "99999999"}) },
		func(e *nostr.Event) { e.Tags = append(e.Tags, nostr.Tag{"title", "duplicate"}) },
		func(e *nostr.Event) { e.Tags[10] = nostr.Tag{"i", cityB} },
	} {
		bad := calendar(original, approval, 3)
		mutate(&bad)
		bad.Sign(admin)
		deny(t, relay, bad)
	}
	accept(t, relay, e)
	count := func() int {
		n := 0
		for range relay.QueryStored(t.Context(), nostr.Filter{Kinds: []nostr.Kind{31923}}) {
			n++
		}
		return n
	}
	if count() != 1 {
		t.Fatal("approved event not public")
	}
	edit := draftEvent(t, creator, cityA, 4)
	accept(t, relay, edit)
	deny(t, relay, calendar(edit, approval, 5))
	rejected := approve(edit, "rejected", 5)
	accept(t, relay, rejected)
	if policy.currentApproval(cityA).ID != approval.ID || count() != 1 {
		t.Fatal("rejected alternative hid current event")
	}
	replacementApproval := approve(edit, "approved", 6)
	accept(t, relay, replacementApproval)
	if count() != 0 {
		t.Fatal("stale calendar remains discoverable")
	}
	deny(t, relay, calendar(original, approval, 7))
	replacement := calendar(edit, replacementApproval, 7)
	accept(t, relay, replacement)
	if count() != 1 {
		t.Fatal("replacement missing")
	}
	prepared := calendar(edit, replacementApproval, 8)
	if err := policy.check(authCtx(t, prepared), prepared); err != nil {
		t.Fatal(err)
	}
	accept(t, relay, approve(edit, "revoked", 9))
	if err := relay.ReplaceEvent(authCtx(t, prepared), prepared); err == nil {
		t.Fatal("revocation raced through storage guard")
	}
	if count() != 0 {
		t.Fatal("revoked calendar remains discoverable")
	}
	for range relay.QueryStored(t.Context(), nostr.Filter{IDs: []nostr.ID{replacement.ID}}) {
		t.Fatal("ID read exposed revoked calendar")
	}
	// Restoring approval never restores the old approval-bound calendar ID.
	restored := approve(edit, "approved", 10)
	accept(t, relay, restored)
	newCalendar := calendar(edit, restored, 11)
	accept(t, relay, newCalendar)
	deletion := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 12, Tags: nostr.Tags{{"e", newCalendar.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "DELETE PUBLISHED WALK: remove the calendar, keep city and history"}
	deletion.Sign(admin)
	badDeletion := deletion
	badDeletion.Sign(creator)
	deny(t, relay, badDeletion)
	badDeletion = deletion
	badDeletion.Tags = nostr.Tags{{"e", original.ID.Hex()}, {"k", "31923"}, {"i", cityA}}
	badDeletion.Sign(admin)
	deny(t, relay, badDeletion)
	badDeletion.Tags = nostr.Tags{{"e", newCalendar.ID.Hex()}, {"k", "31923"}, {"i", cityB}}
	badDeletion.Sign(admin)
	deny(t, relay, badDeletion)
	if _, err := relay.AddEvent(t.Context(), deletion); err == nil {
		t.Fatal("unauthenticated deletion accepted")
	}
	accept(t, relay, deletion)
	if count() != 0 {
		t.Fatal("deleted calendar remained public")
	}
	if policy.currentApproval(cityA).ID != restored.ID || policy.byID(original.ID.Hex()) == nil {
		t.Fatal("deletion altered city authority/history")
	}
	deny(t, relay, newCalendar) // tombstone prevents replay
	for range relay.QueryStored(t.Context(), nostr.Filter{IDs: []nostr.ID{newCalendar.ID}}) {
		t.Fatal("ID lookup exposed deleted event")
	}
	if !relay.PreventBroadcast(nil, nostr.Filter{}, newCalendar) {
		t.Fatal("deleted calendar broadcast allowed")
	}
	if policy.byID(deletion.ID.Hex()) == nil {
		t.Fatal("deletion tombstone not retained")
	}
	fresh := calendar(edit, restored, 13)
	accept(t, relay, fresh)
	if count() != 1 {
		t.Fatal("fresh publication after deletion unavailable")
	}
	// Exercise Khatru's special kind-5 WebSocket path, not just AddEvent.
	server := httptest.NewServer(relay)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := nostr.RelayConnect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deletion.Tags = nostr.Tags{{"e", fresh.ID.Hex()}, {"k", "31923"}, {"i", cityA}}
	deletion.CreatedAt = nostr.Now() + 14
	deletion.Sign(admin)
	if err := client.Publish(ctx, deletion); err == nil {
		t.Fatal("unauthenticated websocket deletion accepted")
	}
	if err := client.Auth(ctx, func(_ context.Context, e *nostr.Event) error { return e.Sign(admin) }); err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(ctx, deletion); err != nil {
		t.Fatal(err)
	}
	if count() != 0 || policy.byID(deletion.ID.Hex()) == nil {
		t.Fatal("websocket deletion not applied")
	}
}

func TestOrganizerOccurrencePublishingAndModeration(t *testing.T) {
	admin, creator, editor, stranger := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, creatorPK, editorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator), nostr.GetPublicKey(editor)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	revision := draftEvent(t, creator, cityA, 0)
	accept(t, relay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex(), editorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 2)
	accept(t, relay, approval)
	occurrence := func(key nostr.SecretKey, offset int) nostr.Event {
		city, e := parseDraft(revision)
		if e != nil {
			t.Fatal(e)
		}
		start := time.Now().Add(48 * time.Hour).Unix()
		event := nostr.Event{Kind: 31923, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Content: city.Description, Tags: nostr.Tags{
			{"d", "123e4567-e89b-42d3-a456-426614174000:2026-09-22"}, {"title", "BitcoinWalk " + city.CityName}, {"summary", "BitcoinWalk in " + city.CityName}, {"image", city.HeroImageURL},
			{"start", strconv.FormatInt(start, 10)}, {"D", strconv.FormatInt(start/86400, 10)}, {"location", "Outside the library"}, {"location", "1.5,2.5"}, {"g", encodeGeohash(1.5, 2.5)}, {"t", "bitcoinwalk"}, {"r", city.ChatURL},
			{"end", strconv.FormatInt(start+3600, 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "occurrence-v1"},
			{"e", revision.ID.Hex(), "", "city-revision"}, {"e", approval.ID.Hex(), "", "city-approval"},
		}}
		event.Sign(key)
		return event
	}
	unauthorized := occurrence(stranger, 3)
	deny(t, relay, unauthorized)
	badGeohash := occurrence(editor, 3)
	for i, tag := range badGeohash.Tags {
		if tag[0] == "g" {
			badGeohash.Tags[i] = nostr.Tag{"g", "9v6kpvcxh"}
		}
	}
	badGeohash.Sign(editor)
	deny(t, relay, badGeohash)
	// BW-56: the inactive replica gate authenticates the forwarding service,
	// while independently checking the original author's city permission.
	service := nostr.Generate()
	scope := replicaScope{CityID: cityA, ServiceKey: nostr.GetPublicKey(service)}
	serviceEvent := nostr.Event{}
	serviceEvent.Sign(service)
	serviceCtx := authCtx(t, serviceEvent)
	if err := policy.checkReplicaOccurrence(serviceCtx, occurrence(editor, 3), scope); err != nil {
		t.Fatalf("valid scoped replica rejected: %v", err)
	}
	for _, test := range []struct {
		name  string
		event nostr.Event
		scope replicaScope
	}{
		{"unauthorized author", unauthorized, scope},
		{"foreign city", occurrence(editor, 3), replicaScope{CityID: cityB, ServiceKey: scope.ServiceKey}},
		{"wrong service", occurrence(editor, 3), replicaScope{CityID: cityA, ServiceKey: creatorPK}},
		{"admin is not service", occurrence(editor, 3), replicaScope{CityID: cityA, ServiceKey: adminPK}},
		{"policy write", approval, scope},
	} {
		t.Run("replica/"+test.name, func(t *testing.T) {
			if policy.checkReplicaOccurrence(serviceCtx, test.event, test.scope) == nil {
				t.Fatal("accepted forbidden replica")
			}
		})
	}
	if policy.checkReplicaOccurrence(t.Context(), occurrence(editor, 3), scope) == nil {
		t.Fatal("unauthenticated replica accepted")
	}
	tampered := occurrence(editor, 3)
	tampered.Content = "tampered"
	if policy.checkReplicaOccurrence(serviceCtx, tampered, scope) == nil {
		t.Fatal("tampered replica accepted")
	}
	event := occurrence(editor, 3)
	if _, err := policy.exportReplicaBundle(cityA, event.ID.Hex()); err == nil {
		t.Fatal("exported event before source acceptance")
	}
	accept(t, relay, event)
	bundle, err := policy.exportReplicaBundle(cityA, event.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateReplicaBundle(serviceCtx, bundle, scope, adminPK); err != nil {
		t.Fatalf("isolated bundle validation: %v", err)
	}
	if err := validateReplicaBundle(t.Context(), bundle, scope, adminPK); err == nil {
		t.Fatal("unauthenticated bundle accepted")
	}
	if err := validateReplicaBundle(serviceCtx, bundle, replicaScope{CityID: cityB, ServiceKey: scope.ServiceKey}, adminPK); err == nil {
		t.Fatal("foreign city bundle accepted")
	}
	var decoded replicaBundle
	if err := json.Unmarshal(bundle, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*replicaBundle)
	}{
		{"missing dependency", func(b *replicaBundle) { b.Events = b.Events[1:] }},
		{"tampered dependency", func(b *replicaBundle) { b.Events[0].Content = "tampered" }},
		{"duplicate dependency", func(b *replicaBundle) { b.Events = append(b.Events, b.Events[0]) }},
		{"missing target", func(b *replicaBundle) { b.OccurrenceID = nostr.Event{}.ID.Hex() }},
		{"wrong version", func(b *replicaBundle) { b.Version = 2 }},
	} {
		t.Run("bundle/"+test.name, func(t *testing.T) {
			var altered replicaBundle
			json.Unmarshal(bundle, &altered)
			test.mutate(&altered)
			data, _ := json.Marshal(altered)
			if validateReplicaBundle(serviceCtx, data, scope, adminPK) == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
	count := func() int {
		n := 0
		for range relay.QueryStored(t.Context(), nostr.Filter{Kinds: []nostr.Kind{31923}}) {
			n++
		}
		return n
	}
	if count() != 1 {
		t.Fatal("organizer occurrence not public")
	}
	otherEditorDeletion := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 4, Tags: nostr.Tags{{"e", event.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "CANCEL WALK: another editor"}
	otherEditorDeletion.Sign(creator)
	deny(t, relay, otherEditorDeletion)
	authorCancellation := otherEditorDeletion
	authorCancellation.Content = "CANCEL WALK: original organizer"
	authorCancellation.Sign(editor)
	accept(t, relay, authorCancellation)
	if count() != 0 || !relay.PreventBroadcast(nil, nostr.Filter{}, event) {
		t.Fatal("organizer-cancelled occurrence remained public")
	}
	deny(t, relay, event) // retained tombstone prevents replay
	if _, err := policy.exportReplicaBundle(cityA, event.ID.Hex()); err == nil {
		t.Fatal("exported cancelled occurrence")
	}
	if policy.checkReplicaOccurrence(serviceCtx, event, scope) == nil {
		t.Fatal("cancelled replica accepted")
	}
	if policy.byID(authorCancellation.ID.Hex()) == nil || policy.byID(event.ID.Hex()) == nil {
		t.Fatal("organizer cancellation did not retain tombstone and history")
	}
	event = occurrence(creator, 5)
	accept(t, relay, event)
	// Removing an editor blocks future writes but preserves already accepted history.
	grant.EditorPubkeys = []string{creatorPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 6))
	if count() != 1 {
		t.Fatal("editor removal erased accepted occurrence")
	}
	deny(t, relay, occurrence(editor, 7))
	if policy.checkReplicaOccurrence(serviceCtx, occurrence(editor, 7), scope) == nil {
		t.Fatal("revoked editor replica accepted")
	}
	deletion := nostr.Event{Kind: 5, CreatedAt: nostr.Now() + 8, Tags: nostr.Tags{{"e", event.ID.Hex()}, {"k", "31923"}, {"i", cityA}}, Content: "DELETE PUBLISHED WALK: super-admin moderation"}
	deletion.Sign(admin)
	accept(t, relay, deletion)
	if count() != 0 || !relay.PreventBroadcast(nil, nostr.Filter{}, event) {
		t.Fatal("moderated organizer occurrence remained public")
	}
	if policy.byID(event.ID.Hex()) == nil {
		t.Fatal("moderation removed retained event history")
	}
	accept(t, relay, workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "revoked"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "revoked"}}, 9))
	if policy.checkReplicaOccurrence(serviceCtx, occurrence(creator, 10), scope) == nil {
		t.Fatal("disapproved city replica accepted")
	}
}

func TestInitialWalkIsHiddenUntilExactApproval(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "initial.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	start := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	proposal := nostr.Event{Kind: 31923, CreatedAt: nostr.Now(), Content: "Test walk", Tags: nostr.Tags{
		{"d", cityA + ":" + start.Format("2006-01-02")}, {"title", "BitcoinWalk Test City"}, {"summary", "BitcoinWalk in Test City"}, {"image", "https://example.com/image.jpg"},
		{"start", strconv.FormatInt(start.Unix(), 10)}, {"D", strconv.FormatInt(start.Unix()/86400, 10)}, {"location", "Square"}, {"location", "1,2"}, {"g", encodeGeohash(1, 2)}, {"t", "bitcoinwalk"}, {"r", "https://example.com/chat"},
		{"end", strconv.FormatInt(start.Add(time.Hour).Unix(), 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "initial-proposal-v1"},
	}}
	proposal.Sign(creator)
	accept(t, relay, proposal)
	count := func() int {
		n := 0
		for range relay.QueryStored(t.Context(), nostr.Filter{Kinds: []nostr.Kind{31923}}) {
			n++
		}
		return n
	}
	if count() != 0 {
		t.Fatal("unapproved initial walk was publicly readable")
	}
	revision := draftEvent(t, creator, cityA, 1)
	var city map[string]any
	if json.Unmarshal([]byte(revision.Content), &city) != nil {
		t.Fatal("draft JSON")
	}
	city["startAt"] = start.Format(time.RFC3339)
	body, _ := json.Marshal(city)
	revision.Content = string(body)
	revision.Tags = append(revision.Tags, nostr.Tag{"e", proposal.ID.Hex(), "", "initial-walk"})
	revision.Sign(creator)
	accept(t, relay, revision)
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 2))
	wrongInitialID := "f" + proposal.ID.Hex()[1:]
	if wrongInitialID == proposal.ID.Hex() {
		wrongInitialID = "e" + proposal.ID.Hex()[1:]
	}
	bad := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), InitialEventID: wrongInitialID, Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"e", wrongInitialID, "", "initial-walk"}, {"status", "approved"}}, 3)
	deny(t, relay, bad)
	approval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), InitialEventID: proposal.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"e", proposal.ID.Hex(), "", "initial-walk"}, {"status", "approved"}}, 4)
	accept(t, relay, approval)
	// An ordinary occurrence for a city whose approval released a first walk
	// must carry that released initial event as a validated dependency.
	if count() != 1 {
		t.Fatal("exact approved initial walk was not released")
	}
	next := proposal
	next.Tags = append(nostr.Tags(nil), proposal.Tags...)
	next.Tags[0] = nostr.Tag{"d", "123e4567-e89b-42d3-a456-426614174000:2026-09-22"}
	for i, tag := range next.Tags {
		if tag[0] == "bitcoinwalk" {
			next.Tags[i] = nostr.Tag{"bitcoinwalk", "occurrence-v1"}
		}
	}
	next.Tags = append(next.Tags, nostr.Tag{"e", revision.ID.Hex(), "", "city-revision"}, nostr.Tag{"e", approval.ID.Hex(), "", "city-approval"})
	next.Sign(creator)
	accept(t, relay, next)
	bundle, err := policy.exportReplicaBundle(cityA, next.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	service := nostr.Event{}
	service.Sign(nostr.Generate())
	if err := validateReplicaBundle(authCtx(t, service), bundle, replicaScope{CityID: cityA, ServiceKey: service.PubKey}, adminPK); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatal("bundle validation changed live source visibility")
	}
	// Approving a later city-profile edit must not hide the exact first walk
	// released by the retained initial approval. Ordinary occurrences already
	// use the same retained-approval read semantics.
	edit := draftEvent(t, creator, cityA, 6)
	accept(t, relay, edit)
	editApproval := workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: edit.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}, {"e", edit.ID.Hex(), "", "city-revision"}, {"status", "approved"}}, 7)
	accept(t, relay, editApproval)
	if count() != 2 {
		t.Fatal("later city-profile approval hid a retained approved occurrence")
	}
}

func TestCalendarGeohashEncoding(t *testing.T) {
	if got := encodeGeohash(30.2672, -97.7431); got != "9v6kpvcxh" {
		t.Fatalf("Austin geohash = %q", got)
	}
	if got := encodeGeohash(35.14332878435158, -90.04102885724478); got != "9ypzzjdhe" {
		t.Fatalf("Memphis geohash = %q", got)
	}
}

func TestPhotoFreeInitialWalkIsAccepted(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK := nostr.GetPublicKey(admin)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "photo-free-initial.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	start := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	proposal := nostr.Event{Kind: 31923, CreatedAt: nostr.Now(), Content: "Test walk", Tags: nostr.Tags{
		{"d", cityA + ":" + start.Format("2006-01-02")}, {"title", "BitcoinWalk Test City"}, {"summary", "BitcoinWalk in Test City"},
		{"start", strconv.FormatInt(start.Unix(), 10)}, {"D", strconv.FormatInt(start.Unix()/86400, 10)}, {"location", "Square"}, {"location", "1,2"}, {"t", "bitcoinwalk"}, {"r", "https://example.com/chat"},
		{"end", strconv.FormatInt(start.Add(time.Hour).Unix(), 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "initial-proposal-v1"},
	}}
	proposal.Sign(creator)
	accept(t, relay, proposal)
}
