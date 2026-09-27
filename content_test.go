package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
)

func contentEvent(t *testing.T, sk nostr.SecretKey, page contentPage, offset int) nostr.Event {
	t.Helper()
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	tags := nostr.Tags{{"d", page.PageID + fmt.Sprintf(":00000000-0000-4000-8000-%012d", offset)}, {"i", page.PageID}, {"page", map[bool]string{true: "/", false: page.Slug}[page.Slug == ""]}, {"status", map[bool]string{true: "published", false: "unpublished"}[page.Published]}, {"client", "bitcoinwalk.org"}}
	if page.PreviousRevisionID != "" {
		tags = append(tags, nostr.Tag{"e", page.PreviousRevisionID, "", "previous"})
	}
	event := nostr.Event{Kind: contentPageKind, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Tags: tags, Content: string(body)}
	if err := event.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return event
}
func testPage(id, slug string) contentPage {
	return contentPage{PageID: id, Slug: slug, Title: "About BitcoinWalk", Eyebrow: "Walk together", Intro: "An introduction", Body: "Body", CTALabel: "Start", CTAHref: "/start", Published: true}
}

func TestContentPublishingPolicy(t *testing.T) {
	admin, other, creator := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK := nostr.GetPublicKey(admin)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	pageID := "88f137cb-2ac1-4eef-8358-7dd66b45922f"
	first := contentEvent(t, admin, testPage(pageID, "about"), 0)
	t.Run("only admin may publish", func(t *testing.T) { deny(t, relay, contentEvent(t, other, testPage(pageID, "about"), 0)) })
	t.Run("admin publishes first revision", func(t *testing.T) { accept(t, relay, first) })
	t.Run("correct predecessor updates URL", func(t *testing.T) {
		page := testPage(pageID, "about-us")
		page.PreviousRevisionID = first.ID.Hex()
		accept(t, relay, contentEvent(t, admin, page, 1))
	})
	t.Run("stale revision is rejected", func(t *testing.T) {
		page := testPage(pageID, "old")
		page.PreviousRevisionID = first.ID.Hex()
		deny(t, relay, contentEvent(t, admin, page, 2))
	})
	t.Run("another page cannot take a live URL", func(t *testing.T) {
		deny(t, relay, contentEvent(t, admin, testPage("99f137cb-2ac1-4eef-8358-7dd66b45922f", "about-us"), 3))
	})
	t.Run("application URL is reserved", func(t *testing.T) {
		deny(t, relay, contentEvent(t, admin, testPage("99f137cb-2ac1-4eef-8358-7dd66b45922f", "admin"), 3))
	})
	t.Run("city URL cannot be reused", func(t *testing.T) {
		accept(t, relay, draftEvent(t, creator, cityA, 0))
		deny(t, relay, contentEvent(t, admin, testPage("99f137cb-2ac1-4eef-8358-7dd66b45922f", "test-city"), 3))
	})
}

func TestHomepageIsPinnedToRoot(t *testing.T) {
	admin := nostr.Generate()
	adminPK := nostr.GetPublicKey(admin)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), map[nostr.PubKey]bool{adminPK: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { relay.DisableExpirationManager(); db.Close() }()
	unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
	home := testPage(homePageID, "")
	accept(t, relay, contentEvent(t, admin, home, 0))
	home.Slug = "home"
	home.PreviousRevisionID = ""
	deny(t, relay, contentEvent(t, admin, home, 1))
}
