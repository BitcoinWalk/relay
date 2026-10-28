package main

import (
	"context"
	"fiatjaf.com/nostr"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignerTransport(t *testing.T) {
	r := newSignerRelay()
	server := httptest.NewServer(r)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	sender, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	receiver, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	key := nostr.Generate()
	recipient := nostr.GetPublicKey(nostr.Generate()).Hex()
	f := nostr.Filter{Kinds: []nostr.Kind{24133}, Tags: nostr.TagMap{"p": []string{recipient}}}
	sub, err := receiver.Subscribe(ctx, f, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsub()
	select {
	case <-sub.EndOfStoredEvents:
	case <-ctx.Done():
		t.Fatal("no EOSE")
	}
	event := nostr.Event{Kind: 24133, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", recipient}}, Content: "opaque encrypted envelope"}
	event.Sign(key)
	if err := sender.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-sub.Events:
		if got.ID != event.ID {
			t.Fatal("wrong event")
		}
	case <-ctx.Done():
		t.Fatal("no live delivery")
	}
	for _, kind := range []nostr.Kind{9, 9007, 1, 22243} {
		bad := event
		bad.Kind = kind
		bad.Sign(key)
		if err := sender.Publish(ctx, bad); err == nil {
			t.Fatalf("accepted %d", kind)
		}
	}
	later, err := receiver.Subscribe(ctx, nostr.Filter{
		Kinds: []nostr.Kind{24133}, Tags: nostr.TagMap{"p": []string{recipient}}, Limit: 1,
	}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer later.Unsub()
	select {
	case got := <-later.Events:
		if got.ID != event.ID {
			t.Fatal("wrong recovery event")
		}
	case <-ctx.Done():
		t.Fatal("recovery replay timeout")
	}
	if r.StoreEvent != nil || r.QueryStored == nil {
		t.Fatal("expected memory-only recovery without persistent storage")
	}
}

func TestSignerReplayExpiresAndStaysBounded(t *testing.T) {
	now := time.Unix(1_000, 0)
	buffer := newSignerReplayBuffer(time.Minute, 2, func() time.Time { return now })
	key := nostr.Generate()
	recipient := nostr.GetPublicKey(nostr.Generate()).Hex()
	makeEvent := func(content string) nostr.Event {
		event := nostr.Event{Kind: 24133, CreatedAt: nostr.Timestamp(now.Unix()), Tags: nostr.Tags{{"p", recipient}}, Content: content}
		event.Sign(key)
		return event
	}
	buffer.add(makeEvent("first"))
	buffer.add(makeEvent("second"))
	buffer.add(makeEvent("third"))

	filter := nostr.Filter{Kinds: []nostr.Kind{24133}, Tags: nostr.TagMap{"p": []string{recipient}}, Limit: 8}
	got := make([]nostr.Event, 0, 2)
	for event := range buffer.query(filter) {
		got = append(got, event)
	}
	if len(got) != 2 || got[0].Content != "third" || got[1].Content != "second" {
		t.Fatalf("unexpected bounded replay: %#v", got)
	}

	now = now.Add(time.Minute + time.Second)
	for range buffer.query(filter) {
		t.Fatal("expired signer envelope was replayed")
	}
}

func TestSignerSubscriptionLimitSupportsAmberConnectionFanout(t *testing.T) {
	r := newSignerRelay()
	server := httptest.NewServer(r)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	receiver, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()

	subscriptions := make([]*nostr.Subscription, 0, signerMaxSubscriptionsPerConnection)
	for i := 0; i < signerMaxSubscriptionsPerConnection; i++ {
		recipient := nostr.GetPublicKey(nostr.Generate()).Hex()
		sub, err := receiver.Subscribe(ctx, nostr.Filter{
			Kinds: []nostr.Kind{24133},
			Tags:  nostr.TagMap{"p": []string{recipient}},
		}, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatalf("subscribe %d: %v", i+1, err)
		}
		subscriptions = append(subscriptions, sub)
		select {
		case <-sub.EndOfStoredEvents:
		case reason := <-sub.ClosedReason:
			t.Fatalf("subscription %d rejected before the bounded limit: %s", i+1, reason)
		case <-ctx.Done():
			t.Fatalf("subscription %d did not become active", i+1)
		}
	}
	defer func() {
		for _, sub := range subscriptions {
			sub.Unsub()
		}
	}()

	overflow, err := receiver.Subscribe(ctx, nostr.Filter{
		Kinds: []nostr.Kind{24133},
		Tags:  nostr.TagMap{"p": []string{nostr.GetPublicKey(nostr.Generate()).Hex()}},
	}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer overflow.Unsub()
	select {
	case reason := <-overflow.ClosedReason:
		if !strings.Contains(reason, "rate-limited: subscriptions") {
			t.Fatalf("unexpected overflow rejection: %s", reason)
		}
	case <-overflow.EndOfStoredEvents:
		t.Fatal("accepted a subscription beyond the bounded per-connection limit")
	case <-ctx.Done():
		t.Fatal("overflow subscription was not rejected")
	}
}

func TestSignerRejectsInvalidEnvelopesAndBroadReads(t *testing.T) {
	r := newSignerRelay()
	ctx := context.Background()
	key := nostr.Generate()
	e := nostr.Event{Kind: 24133, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", nostr.GetPublicKey(key).Hex()}}, Content: "payload"}
	e.Sign(key)
	for _, mutate := range []func(*nostr.Event){
		func(e *nostr.Event) { e.Content = "" }, func(e *nostr.Event) { e.Tags = nil }, func(e *nostr.Event) { e.CreatedAt -= 1000 }, func(e *nostr.Event) { e.Content = strings.Repeat("a", 32769) },
	} {
		bad := e
		mutate(&bad)
		bad.Sign(key)
		if reject, _ := r.OnEvent(ctx, bad); !reject {
			t.Fatal("invalid envelope accepted")
		}
	}
	e.Content = "tampered"
	if reject, _ := r.OnEvent(ctx, e); !reject {
		t.Fatal("bad signature accepted")
	}
	if reject, _ := r.OnRequest(ctx, nostr.Filter{}); !reject {
		t.Fatal("broad read accepted")
	}
}

func TestSignerTransportModeValidation(t *testing.T) {
	if err := runSignerTransport("invalid"); err == nil || !strings.Contains(err.Error(), "must be staging or remote") {
		t.Fatalf("unexpected mode validation: %v", err)
	}
	t.Setenv("RELAY_SIGNER_LISTEN", "0.0.0.0:3344")
	if err := runSignerTransport("remote"); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("unexpected listen validation: %v", err)
	}
}
