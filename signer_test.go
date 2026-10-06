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
	later, err := receiver.Subscribe(ctx, f, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer later.Unsub()
	select {
	case <-later.Events:
		t.Fatal("ephemeral event persisted")
	case <-later.EndOfStoredEvents:
	case <-ctx.Done():
		t.Fatal("history timeout")
	}
	if r.StoreEvent != nil || r.QueryStored != nil {
		t.Fatal("unexpected storage")
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
