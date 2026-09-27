package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
)

const cityA = "66f137cb-2ac1-4eef-8358-7dd66b45922f"
const cityB = "77f137cb-2ac1-4eef-8358-7dd66b45922f"

func workflowEvent(t *testing.T, sk nostr.SecretKey, kind nostr.Kind, content any, tags nostr.Tags, offset int) nostr.Event {
	t.Helper()
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if kind == 30303 || kind == 30304 {
		tags = append(nostr.Tags(nil), tags...)
		for i, tag := range tags {
			if len(tag) > 1 && tag[0] == "d" {
				tags[i] = nostr.Tag{"d", tag[1] + fmt.Sprintf(":00000000-0000-4000-8000-%012d", offset)}
				tags = append(tags, nostr.Tag{"i", tag[1]})
				break
			}
		}
	}
	event := nostr.Event{Kind: kind, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Tags: tags, Content: string(data)}
	if err := event.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return event
}
func draftEvent(t *testing.T, sk nostr.SecretKey, id string, offset int) nostr.Event {
	return workflowEvent(t, sk, 30303, map[string]any{"cityId": id, "slug": "test-city", "cityName": "Test City", "startAt": "2026-10-03T10:00:00Z", "description": "Test walk", "meetingPoint": map[string]any{"description": "Square", "latitude": 1, "longitude": 2}, "chatUrl": "https://example.com/chat", "heroImageUrl": "https://example.com/image.jpg"}, nostr.Tags{{"d", id}, {"city", "test-city"}}, offset)
}

func TestDraftHeroImageIsOptionalButValidated(t *testing.T) {
	sk := nostr.Generate()
	base := map[string]any{"cityId": cityA, "slug": "test-city", "cityName": "Test City", "startAt": "2026-10-03T10:00:00Z", "description": "Test walk", "meetingPoint": map[string]any{"description": "Square", "latitude": 1, "longitude": 2}, "chatUrl": "https://example.com/chat"}
	without := workflowEvent(t, sk, 30303, base, nostr.Tags{{"d", cityA}, {"city", "test-city"}}, 0)
	if _, err := parseDraft(without); err != nil {
		t.Fatalf("photo-free city submission rejected: %v", err)
	}
	base["heroImageUrl"] = "javascript:alert(1)"
	invalid := workflowEvent(t, sk, 30303, base, nostr.Tags{{"d", cityA}, {"city", "test-city"}}, 1)
	if _, err := parseDraft(invalid); err == nil {
		t.Fatal("invalid optional hero URL accepted")
	}
}

func TestDraftCityAliasesAreOptionalBoundedAndUnique(t *testing.T) {
	sk := nostr.Generate()
	base := map[string]any{"cityId": cityA, "slug": "warszawa", "cityName": "Warszawa", "startAt": "2026-10-03T10:00:00Z", "description": "Test walk", "meetingPoint": map[string]any{"description": "Square", "latitude": 1, "longitude": 2}, "chatUrl": "https://example.com/chat", "aliases": []string{"Warsaw", "Warschau"}}
	valid := workflowEvent(t, sk, 30303, base, nostr.Tags{{"d", cityA}, {"city", "warszawa"}}, 0)
	if _, err := parseDraft(valid); err != nil {
		t.Fatalf("valid aliases rejected: %v", err)
	}
	base["aliases"] = []string{"Warsaw", "warsaw"}
	duplicate := workflowEvent(t, sk, 30303, base, nostr.Tags{{"d", cityA}, {"city", "warszawa"}}, 1)
	if _, err := parseDraft(duplicate); err == nil {
		t.Fatal("case-insensitive duplicate alias accepted")
	}
	base["aliases"] = []string{"Warszawa"}
	canonical := workflowEvent(t, sk, 30303, base, nostr.Tags{{"d", cityA}, {"city", "warszawa"}}, 2)
	if _, err := parseDraft(canonical); err == nil {
		t.Fatal("canonical city name accepted as an alias")
	}
}

func TestRelayMetadataCanBeConfigured(t *testing.T) {
	t.Setenv("RELAY_NAME", "BitcoinWalk production relay")
	t.Setenv("RELAY_DESCRIPTION", "Shared relay for free BitcoinWalk cities.")
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	if relay.Info.Name != "BitcoinWalk production relay" || relay.Info.Description != "Shared relay for free BitcoinWalk cities." {
		t.Fatalf("unexpected relay metadata: %#v", relay.Info)
	}
}
func authCtx(t *testing.T, event nostr.Event) context.Context {
	return khatru.ForceSetAuthed(t.Context(), event.PubKey)
}

// Direct policy tests have no HTTP request/IP. Avoid sharing the production
// limiter's empty-IP bucket across unrelated tests. WebSocket tests below keep
// the complete production OnEvent chain; storage checks are unchanged here.
func unitOrganizerPolicy(relay *khatru.Relay, policy *organizerPolicy) *organizerPolicy {
	relay.OnEvent = func(ctx context.Context, event nostr.Event) (bool, string) {
		policy.mu.Lock()
		defer policy.mu.Unlock()
		if err := policy.check(ctx, event); err != nil {
			return true, err.Error()
		}
		return false, ""
	}
	return policy
}
func accept(t *testing.T, relay *khatru.Relay, event nostr.Event) {
	t.Helper()
	if _, err := relay.AddEvent(authCtx(t, event), event); err != nil {
		t.Fatal(err)
	}
}
func deny(t *testing.T, relay *khatru.Relay, event nostr.Event) {
	t.Helper()
	if _, err := relay.AddEvent(authCtx(t, event), event); err == nil {
		t.Fatal("unauthorized event accepted")
	}
}

func TestOrganizerLifecycle(t *testing.T) {
	admin, creator, editor, outsider := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, creatorPK, editorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator), nostr.GetPublicKey(editor)
	path := filepath.Join(t.TempDir(), "events.db")
	relay, db, err := newRelay(path, map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	policy := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	proposal := draftEvent(t, creator, cityA, 0)
	t.Run("requires matching authentication", func(t *testing.T) {
		if _, err := relay.AddEvent(t.Context(), proposal); err == nil {
			t.Fatal("missing auth accepted")
		}
		if _, err := relay.AddEvent(khatru.ForceSetAuthed(t.Context(), adminPK), proposal); err == nil {
			t.Fatal("wrong auth accepted")
		}
	})
	t.Run("new organizer can submit", func(t *testing.T) { accept(t, relay, proposal) })
	t.Run("one identity cannot flood multiple pending cities", func(t *testing.T) {
		deny(t, relay, draftEvent(t, creator, cityB, 1))
	})
	grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: proposal.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex(), editorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
	grantEvent := workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 1)
	t.Run("organizer cannot grant access", func(t *testing.T) {
		deny(t, relay, workflowEvent(t, creator, 30302, grant, nostr.Tags{{"d", cityA}}, 1))
	})
	t.Run("creator must match original", func(t *testing.T) {
		bad := grant
		bad.CreatorPubkey = editorPK.Hex()
		deny(t, relay, workflowEvent(t, admin, 30302, bad, nostr.Tags{{"d", cityA}}, 1))
	})
	t.Run("admin registers creator and editors", func(t *testing.T) { accept(t, relay, grantEvent) })
	t.Run("creator and editor may edit", func(t *testing.T) {
		accept(t, relay, draftEvent(t, creator, cityA, 2))
		accept(t, relay, draftEvent(t, editor, cityA, 2))
	})
	t.Run("other organizer cannot edit registered city", func(t *testing.T) {
		deny(t, relay, draftEvent(t, outsider, cityA, 3))
		accept(t, relay, draftEvent(t, outsider, cityB, 0))
	})
	t.Run("super-admin edits any city", func(t *testing.T) { accept(t, relay, draftEvent(t, admin, cityA, 3)) })
	rev := draftEvent(t, creator, cityA, 4)
	accept(t, relay, rev)
	decision := cityDecision{CityID: cityA, RevisionID: rev.ID.Hex(), Status: "approved"}
	approvalTags := nostr.Tags{{"d", cityA}, {"e", rev.ID.Hex(), "", "city-revision"}, {"status", "approved"}}
	t.Run("organizer cannot approve", func(t *testing.T) { deny(t, relay, workflowEvent(t, creator, 30304, decision, approvalTags, 5)) })
	t.Run("admin approves exact city revision", func(t *testing.T) { accept(t, relay, workflowEvent(t, admin, 30304, decision, approvalTags, 5)) })
	t.Run("invalid approval slug is rejected", func(t *testing.T) {
		bad := decision
		bad.Slug = "szyd-owiec!"
		tags := nostr.Tags{{"d", cityA}, {"e", rev.ID.Hex(), "", "city-revision"}, {"status", "approved"}, {"city", bad.Slug}}
		deny(t, relay, workflowEvent(t, admin, 30304, bad, tags, 6))
	})
	t.Run("cross-city approval rejected", func(t *testing.T) {
		bad := decision
		bad.CityID = cityB
		tags := nostr.Tags{{"d", cityB}, {"e", rev.ID.Hex(), "", "city-revision"}, {"status", "approved"}}
		deny(t, relay, workflowEvent(t, admin, 30304, bad, tags, 6))
	})
	pending := draftEvent(t, editor, cityA, 6)
	t.Run("editor passes precheck before revocation", func(t *testing.T) {
		if err := policy.check(authCtx(t, pending), pending); err != nil {
			t.Fatal(err)
		}
	})
	grant.EditorPubkeys = []string{creatorPK.Hex()}
	revoked := workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 7)
	accept(t, relay, revoked)
	t.Run("revocation rechecked at storage boundary", func(t *testing.T) {
		if err := relay.ReplaceEvent(authCtx(t, pending), pending); err == nil {
			t.Fatal("revoked editor stored after precheck")
		}
	})
	t.Run("revoked editor denied but creator retains access", func(t *testing.T) {
		deny(t, relay, draftEvent(t, editor, cityA, 8))
		accept(t, relay, draftEvent(t, creator, cityA, 8))
	})
	t.Run("stale grant cannot restore access", func(t *testing.T) { deny(t, relay, grantEvent) })
	t.Run("creator cannot be removed", func(t *testing.T) {
		bad := grant
		bad.EditorPubkeys = []string{editorPK.Hex()}
		deny(t, relay, workflowEvent(t, admin, 30302, bad, nostr.Tags{{"d", cityA}}, 9))
	})
	t.Run("authority cannot expire or be deleted", func(t *testing.T) {
		deny(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}, {"expiration", "9999999999"}}, 9))
		deny(t, relay, workflowEvent(t, admin, 5, "", nostr.Tags{{"e", revoked.ID.Hex()}}, 9))
	})
	t.Run("revocation survives restart", func(t *testing.T) {
		relay.DisableExpirationManager()
		db.Close()
		relay, db, err = newRelay(path, map[nostr.PubKey]bool{adminPK: true})
		if err != nil {
			t.Fatal(err)
		}
		unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
		deny(t, relay, draftEvent(t, editor, cityA, 10))
		accept(t, relay, draftEvent(t, creator, cityA, 10))
	})
	t.Run("admin can revoke replaced revision approval", func(t *testing.T) {
		decision.Status = "revoked"
		tags := nostr.Tags{{"d", cityA}, {"e", rev.ID.Hex(), "", "city-revision"}, {"status", "revoked"}}
		accept(t, relay, workflowEvent(t, admin, 30304, decision, tags, 11))
	})
}

func TestOrganizerValidation(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK := nostr.GetPublicKey(admin)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	enableOrganizers(relay, db, adminPK)
	for _, tc := range []struct {
		name   string
		mutate func(*nostr.Event)
	}{
		{"wrong d", func(e *nostr.Event) { e.Tags[0][1] = cityB }},
		{"duplicate d", func(e *nostr.Event) { e.Tags = append(e.Tags, nostr.Tag{"d", cityA}) }},
		{"wrong slug tag", func(e *nostr.Event) { e.Tags[1][1] = "other-city" }},
		{"malformed body", func(e *nostr.Event) { e.Content = `{"cityId":"bad"}` }},
		{"future timestamp", func(e *nostr.Event) { e.CreatedAt = nostr.Now() + 3600 }},
		{"calendar blocked until approved publishing exists", func(e *nostr.Event) { e.Kind = 31923 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := draftEvent(t, creator, cityA, 0)
			tc.mutate(&event)
			event.Sign(creator)
			deny(t, relay, event)
		})
	}
}

func TestOrganizerWebSocketSubmission(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	adminPK := nostr.GetPublicKey(admin)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	enableOrganizers(relay, db, adminPK)
	server := httptest.NewServer(relay)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := nostr.RelayConnect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	event := draftEvent(t, creator, cityA, 0)
	if err := client.Publish(ctx, event); err == nil || !strings.Contains(err.Error(), "auth-required:") {
		t.Fatalf("expected AUTH, got %v", err)
	}
	if err := client.Auth(ctx, func(_ context.Context, e *nostr.Event) error { return e.Sign(creator) }); err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	forged := workflowEvent(t, creator, 30304, cityDecision{CityID: cityA, RevisionID: event.ID.Hex(), Status: "approved"}, nostr.Tags{{"d", cityA}}, 0)
	if err := client.Publish(ctx, forged); err == nil {
		t.Fatal("organizer approved own event")
	}
}
