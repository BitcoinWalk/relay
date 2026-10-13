package main

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
	"github.com/BitcoinWalk/relay/internal/chatstate"
)

func TestCombinedChatRoutesWithoutReplacingCityEventHooks(t *testing.T) {
	store, err := chatstate.OpenSigned(filepath.Join(t.TempDir(), "chat.db"), nostr.MustPubKeyFromHex(adminHex), nostr.Generate())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	relay := khatru.NewRelay()
	relay.Negentropy = true
	relay.RejectConnection = func(request *http.Request) bool {
		return request.Header.Get("X-Reject-City-Connection") == "true"
	}
	eventChecked, eventStored, eventQueried := false, false, false
	relay.OnEvent = func(context.Context, nostr.Event) (bool, string) {
		eventChecked = true
		return false, ""
	}
	relay.StoreEvent = func(context.Context, nostr.Event) error {
		eventStored = true
		return nil
	}
	relay.OnRequest = func(context.Context, nostr.Filter) (bool, string) { return false, "" }
	relay.OnCount = func(context.Context, nostr.Filter) (bool, string) { return false, "" }
	relay.QueryStored = func(context.Context, nostr.Filter) iter.Seq[nostr.Event] {
		return func(yield func(nostr.Event) bool) {
			eventQueried = true
			yield(nostr.Event{Kind: 31923})
		}
	}
	if err := attachChatRelay(relay, store); err != nil {
		t.Fatal(err)
	}
	if !relay.Negentropy {
		t.Fatal("combined chat disabled the city relay's existing negentropy capability")
	}
	request := httptest.NewRequest(http.MethodGet, "http://relay.test/", nil)
	request.Header.Set("X-Reject-City-Connection", "true")
	if relay.RejectConnection == nil || !relay.RejectConnection(request) {
		t.Fatal("combined chat replaced the city relay's connection policy")
	}

	if rejected, _ := relay.OnEvent(t.Context(), nostr.Event{Kind: 31923}); rejected || !eventChecked {
		t.Fatal("city event policy was not preserved")
	}
	if err := relay.StoreEvent(t.Context(), nostr.Event{Kind: 31923}); err != nil || !eventStored {
		t.Fatal("city event storage was not preserved")
	}
	for range relay.QueryStored(t.Context(), nostr.Filter{Kinds: []nostr.Kind{31923}}) {
	}
	if !eventQueried {
		t.Fatal("city event query was not preserved")
	}

	eventChecked, eventStored, eventQueried = false, false, false
	if rejected, reason := relay.OnEvent(t.Context(), nostr.Event{Kind: 9021}); !rejected || !strings.Contains(reason, "auth-required") || eventChecked {
		t.Fatal("chat command did not remain inside the chat policy")
	}
	if err := relay.StoreEvent(t.Context(), nostr.Event{Kind: 9021}); err == nil || eventStored {
		t.Fatal("chat command reached city event storage")
	}
	for range relay.QueryStored(t.Context(), nostr.Filter{Kinds: []nostr.Kind{39000}}) {
	}
	if eventQueried {
		t.Fatal("chat metadata query reached city event storage")
	}
	if rejected, _ := relay.OnCount(t.Context(), nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": {"london"}}}); !rejected {
		t.Fatal("chat count was enabled")
	}
	if relay.Info.Self == nil {
		t.Fatal("combined relay did not advertise its NIP-29 identity")
	}
	found := false
	for _, nip := range relay.Info.SupportedNIPs {
		found = found || nip == 29
	}
	if !found {
		t.Fatal("combined relay did not advertise NIP-29")
	}
}

func TestCombinedChatAndCityEventWebSocket(t *testing.T) {
	admin, member := nostr.Generate(), nostr.Generate()
	store, err := chatstate.OpenSigned(filepath.Join(t.TempDir(), "chat.db"), nostr.GetPublicKey(admin), nostr.Generate())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	event := nostr.Event{Kind: 31923, CreatedAt: nostr.Now(), Content: "city event"}
	if err := event.Sign(admin); err != nil {
		t.Fatal(err)
	}
	relay := khatru.NewRelay()
	relay.OnEvent = func(context.Context, nostr.Event) (bool, string) { return false, "" }
	relay.StoreEvent = func(context.Context, nostr.Event) error { return nil }
	relay.OnRequest = func(context.Context, nostr.Filter) (bool, string) { return false, "" }
	relay.OnCount = func(context.Context, nostr.Filter) (bool, string) { return false, "" }
	relay.QueryStored = func(_ context.Context, filter nostr.Filter) iter.Seq[nostr.Event] {
		return func(yield func(nostr.Event) bool) {
			if filter.Matches(event) {
				yield(event)
			}
		}
	}
	if err := attachChatRelay(relay, store); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(relay)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	connect := func(key nostr.SecretKey) *nostr.Relay {
		client, err := nostr.RelayConnect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nostr.RelayOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		probe, err := client.Subscribe(ctx, nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": {"london"}}}, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-probe.ClosedReason:
		case <-ctx.Done():
			t.Fatal("missing combined chat authentication challenge")
		}
		probe.Unsub()
		if err := client.Auth(ctx, func(_ context.Context, auth *nostr.Event) error { return auth.Sign(key) }); err != nil {
			t.Fatal(err)
		}
		return client
	}
	owner, user := connect(admin), connect(member)
	chatEvent := func(key nostr.SecretKey, kind nostr.Kind, content string) nostr.Event {
		e := nostr.Event{Kind: kind, CreatedAt: nostr.Now(), Content: content, Tags: nostr.Tags{{"h", "london"}}}
		if err := e.Sign(key); err != nil {
			t.Fatal(err)
		}
		return e
	}
	if err := owner.Publish(ctx, chatEvent(admin, 9007, "create")); err != nil {
		t.Fatal(err)
	}
	if err := user.Publish(ctx, chatEvent(member, 9021, "join")); err != nil {
		t.Fatal(err)
	}
	message := chatEvent(member, 9, "hello London")
	if err := user.Publish(ctx, message); err != nil {
		t.Fatal(err)
	}
	history := make([]nostr.Event, 0, 1)
	for got := range user.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": {"london"}}}) {
		history = append(history, got)
	}
	if len(history) != 1 || history[0].ID != message.ID {
		t.Fatalf("combined chat history failed: events=%d", len(history))
	}
	cityEvents := make([]nostr.Event, 0, 1)
	for got := range user.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{31923}}) {
		cityEvents = append(cityEvents, got)
	}
	if len(cityEvents) != 1 || cityEvents[0].ID != event.ID {
		t.Fatalf("combined city event read failed: events=%d", len(cityEvents))
	}
}

func TestCombinedChatConfigurationFailsClosed(t *testing.T) {
	relay := khatru.NewRelay()
	t.Setenv("RELAY_CHAT_COMBINED", "true")
	t.Setenv("RELAY_CHAT_URL", "wss://london.bitcoinwalk.org/")
	t.Setenv("RELAY_CHAT_DB", filepath.Join(t.TempDir(), "chat.db"))
	t.Setenv("RELAY_CHAT_KEY_FILE", filepath.Join(t.TempDir(), "missing-key"))
	if _, err := enableCombinedCityChat(relay); err == nil {
		t.Fatal("missing combined-chat identity was accepted")
	}

	t.Setenv("RELAY_CHAT_URL", "wss://london.bitcoinwalk.org/path")
	if _, err := enableCombinedCityChat(relay); err == nil {
		t.Fatal("path-scoped canonical endpoint was accepted")
	}
}
