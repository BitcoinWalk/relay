package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
)

func signedGiftWrap(t *testing.T, recipient nostr.PubKey, createdAt time.Time) nostr.Event {
	t.Helper()
	event := nostr.Event{
		Kind: inboxGiftWrapKind, CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Tags: nostr.Tags{{"p", recipient.Hex()}}, Content: "encrypted-gift-wrap",
	}
	if err := event.Sign(nostr.Generate()); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestInboxPolicyRequiresAuthenticationAndOneRecipient(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	policy := inboxPolicy{limits: &chatLimits{}, now: func() time.Time { return now }}
	sender := nostr.GetPublicKey(nostr.Generate())
	recipient := nostr.GetPublicKey(nostr.Generate())
	event := signedGiftWrap(t, recipient, now.Add(-2*time.Hour))

	if reject, reason := policy.event(t.Context(), event); !reject || !strings.HasPrefix(reason, "auth-required:") {
		t.Fatalf("unauthenticated publish was not challenged: %v %q", reject, reason)
	}
	authed := khatru.ForceSetAuthed(t.Context(), sender)
	if reject, reason := policy.event(authed, event); reject {
		t.Fatalf("authenticated gift wrap rejected: %s", reason)
	}

	wrongKind := event
	wrongKind.Kind = 1
	if reject, _ := policy.event(authed, wrongKind); !reject {
		t.Fatal("non-gift-wrap event accepted")
	}
	duplicateRecipient := event
	duplicateRecipient.Tags = append(duplicateRecipient.Tags, nostr.Tag{"p", sender.Hex()})
	if reject, _ := policy.event(authed, duplicateRecipient); !reject {
		t.Fatal("gift wrap with multiple recipients accepted")
	}
	foreignTag := event
	foreignTag.Tags = append(foreignTag.Tags, nostr.Tag{"e", strings.Repeat("a", 64)})
	if reject, _ := policy.event(authed, foreignTag); !reject {
		t.Fatal("gift wrap with metadata-bearing tag accepted")
	}
	stale := signedGiftWrap(t, recipient, now.Add(-inboxRetention-time.Second))
	if reject, _ := policy.event(authed, stale); !reject {
		t.Fatal("gift wrap outside retention accepted")
	}
}

func TestInboxFilterIsRecipientScoped(t *testing.T) {
	recipient := nostr.GetPublicKey(nostr.Generate())
	other := nostr.GetPublicKey(nostr.Generate())
	filter := nostr.Filter{Kinds: []nostr.Kind{inboxGiftWrapKind}, Tags: nostr.TagMap{"p": {recipient.Hex()}}, Limit: 50}

	if _, ok, reason := inboxFilterRecipient(t.Context(), filter); ok || !strings.HasPrefix(reason, "auth-required:") {
		t.Fatalf("unauthenticated inbox read was not challenged: %v %q", ok, reason)
	}
	if _, ok, reason := inboxFilterRecipient(khatru.ForceSetAuthed(t.Context(), other), filter); ok || !strings.HasPrefix(reason, "restricted:") {
		t.Fatalf("foreign inbox read was not rejected: %v %q", ok, reason)
	}
	if got, ok, reason := inboxFilterRecipient(khatru.ForceSetAuthed(t.Context(), recipient), filter); !ok || got != recipient || reason != "" {
		t.Fatalf("recipient inbox rejected: %v %v %q", got, ok, reason)
	}
	idsOnly := nostr.Filter{IDs: []nostr.ID{signedGiftWrap(t, recipient, time.Now()).ID}}
	if _, ok, _ := inboxFilterRecipient(khatru.ForceSetAuthed(t.Context(), recipient), idsOnly); ok {
		t.Fatal("ID-only query could bypass recipient scope")
	}
}

func TestInboxWebSocketRecipientReadAndPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "inbox.db")
	relay, db, err := newInboxRelay(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(relay)
	closed := false
	defer func() {
		if !closed {
			server.Close()
			relay.DisableExpirationManager()
			db.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http")

	senderSecret := nostr.Generate()
	sender, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	recipientSecret := nostr.Generate()
	recipientKey := nostr.GetPublicKey(recipientSecret)
	event := signedGiftWrap(t, recipientKey, time.Now().Add(-time.Hour))
	if err := sender.Publish(ctx, event); err == nil || !strings.Contains(err.Error(), "auth-required:") {
		t.Fatalf("expected authentication challenge, got %v", err)
	}
	if err := sender.Auth(ctx, func(_ context.Context, event *nostr.Event) error { return event.Sign(senderSecret) }); err != nil {
		t.Fatal(err)
	}
	if err := sender.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}

	recipient, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer recipient.Close()
	filter := nostr.Filter{
		Kinds: []nostr.Kind{inboxGiftWrapKind}, Tags: nostr.TagMap{"p": {recipientKey.Hex()}}, Limit: 20,
	}
	challengeSub, err := recipient.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-challengeSub.ClosedReason:
		if !strings.Contains(reason, "auth-required:") {
			t.Fatalf("expected recipient authentication challenge, got %q", reason)
		}
	case <-ctx.Done():
		t.Fatal("recipient read did not trigger authentication challenge")
	}
	if err := recipient.Auth(ctx, func(_ context.Context, event *nostr.Event) error { return event.Sign(recipientSecret) }); err != nil {
		t.Fatal(err)
	}
	sub, err := recipient.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-sub.Events:
		if got.ID != event.ID {
			t.Fatalf("wrong gift wrap returned: %s", got.ID.Hex())
		}
	case <-ctx.Done():
		t.Fatal("recipient could not read stored gift wrap")
	}
	sub.Unsub()

	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	request.Header.Set("Accept", "application/nostr+json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	err = json.NewDecoder(response.Body).Decode(&info)
	response.Body.Close()
	if err != nil || info["name"] != "BitcoinWalk private message inbox" {
		t.Fatalf("unexpected NIP-11 response: %v %v", info, err)
	}

	recipient.Close()
	sender.Close()
	server.Close()
	relay.DisableExpirationManager()
	db.Close()
	closed = true

	reopened, stored, err := newInboxRelay(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.DisableExpirationManager()
	defer stored.Close()
	found := false
	for got := range stored.QueryEvents(nostr.Filter{IDs: []nostr.ID{event.ID}}, 1) {
		found = got.ID == event.ID
	}
	if !found {
		t.Fatal("gift wrap did not survive database reopen")
	}
}

func TestInboxRetentionDeletesOnlyExpiredGiftWraps(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "inbox.db")
	relay, db, err := newInboxRelay(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.DisableExpirationManager()
	defer db.Close()
	now := time.Unix(1_800_000_000, 0)
	recipient := nostr.GetPublicKey(nostr.Generate())
	old := signedGiftWrap(t, recipient, now.Add(-inboxRetention-time.Hour))
	fresh := signedGiftWrap(t, recipient, now.Add(-time.Hour))
	if err := db.SaveEvent(old); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEvent(fresh); err != nil {
		t.Fatal(err)
	}
	removed, err := pruneInbox(db, now)
	if err != nil || removed != 1 {
		t.Fatalf("unexpected retention result: removed=%d err=%v", removed, err)
	}
	remaining := 0
	for range db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{inboxGiftWrapKind}}, 10) {
		remaining++
	}
	if remaining != 1 {
		t.Fatalf("unexpected remaining gift wraps: %d", remaining)
	}
}
