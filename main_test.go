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

func signedEvent(t *testing.T, sk nostr.SecretKey, kind nostr.Kind, content string) nostr.Event {
	t.Helper()
	evt := nostr.Event{CreatedAt: nostr.Now(), Kind: kind, Tags: nostr.Tags{{"d", "test-walk"}}, Content: content}
	if err := evt.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return evt
}

func TestWriterPolicy(t *testing.T) {
	sk := nostr.Generate()
	pk := nostr.GetPublicKey(sk)
	policy := writePolicy(map[nostr.PubKey]bool{pk: true})
	evt := signedEvent(t, sk, 31923, "test")
	if reject, reason := policy(t.Context(), evt); !reject || !strings.HasPrefix(reason, "auth-required:") {
		t.Fatalf("expected auth requirement: %v %s", reject, reason)
	}
	ctx := khatru.ForceSetAuthed(t.Context(), pk)
	if reject, reason := policy(ctx, evt); reject {
		t.Fatal(reason)
	}
	evt.Kind = 1
	if reject, _ := policy(ctx, evt); !reject {
		t.Fatal("unsupported kind accepted")
	}
	other := signedEvent(t, nostr.Generate(), 31923, "other")
	if reject, _ := policy(ctx, other); !reject {
		t.Fatal("unauthorized author accepted")
	}
	if _, err := parseWriters("not-a-public-key"); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	if _, err := parseWriters(""); err == nil {
		t.Fatal("empty configuration accepted")
	}
}

func TestListenAddressRequiresExplicitDirectoryContainerMode(t *testing.T) {
	for _, test := range []struct {
		name      string
		listen    string
		container bool
		accepted  bool
	}{
		{"host loopback", "127.0.0.1:3343", false, true},
		{"generic wildcard", "0.0.0.0:3343", false, false},
		{"directory container wildcard", "0.0.0.0:3343", true, true},
		{"container IPv6 wildcard", "[::]:3343", true, false},
		{"hostname", "localhost:3343", true, false},
		{"malformed", "3343", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateListenAddress(test.listen, test.container)
			if test.accepted && err != nil {
				t.Fatal(err)
			}
			if !test.accepted && err == nil {
				t.Fatal("unsafe listen address accepted")
			}
		})
	}
}

func TestReplicaReceiverContainerListenRequiresCompleteIsolatedScope(t *testing.T) {
	complete := []string{"true", "true", "be8514a4-9df0-4159-a517-71f65761cbbe", strings.Repeat("a", 64), "wss://replica.bitcoinwalk.org/"}
	allowed, err := replicaReceiverContainerListenAllowed(complete[0], complete[1], complete[2], complete[3], complete[4])
	if err != nil || !allowed {
		t.Fatalf("complete receiver scope rejected: %v", err)
	}
	for name, values := range map[string][]string{
		"invalid flag":    {"yes", complete[1], complete[2], complete[3], complete[4]},
		"organizer off":   {complete[0], "false", complete[2], complete[3], complete[4]},
		"missing city":    {complete[0], complete[1], "", complete[3], complete[4]},
		"missing service": {complete[0], complete[1], complete[2], "", complete[4]},
		"missing target":  {complete[0], complete[1], complete[2], complete[3], ""},
	} {
		t.Run(name, func(t *testing.T) {
			if allowed, err := replicaReceiverContainerListenAllowed(values[0], values[1], values[2], values[3], values[4]); err == nil || allowed {
				t.Fatal("unsafe receiver container listen accepted")
			}
		})
	}
	if allowed, err := replicaReceiverContainerListenAllowed("false", "false", "", "", ""); err != nil || allowed {
		t.Fatalf("disabled receiver container mode rejected: %v", err)
	}
}

func TestWebSocketAuthPublicReadAndPersistence(t *testing.T) {
	sk := nostr.Generate()
	pk := nostr.GetPublicKey(sk)
	writers := map[nostr.PubKey]bool{pk: true}
	path := filepath.Join(t.TempDir(), "events.db")
	relay, db, err := newRelay(path, writers)
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
	writer, err := nostr.RelayConnect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	evt := signedEvent(t, sk, 31923, "BitcoinWalk test event")
	if err := writer.Publish(ctx, evt); err == nil || !strings.Contains(err.Error(), "auth-required:") {
		t.Fatalf("expected auth rejection, got %v", err)
	}
	if err := writer.Auth(ctx, func(ctx context.Context, event *nostr.Event) error { return event.Sign(sk) }); err != nil {
		t.Fatal(err)
	}
	if err := writer.Publish(ctx, evt); err != nil {
		t.Fatal(err)
	}
	other := signedEvent(t, nostr.Generate(), 31923, "unauthorized")
	if err := writer.Publish(ctx, other); err == nil {
		t.Fatal("unauthorized event accepted")
	}
	invalid := signedEvent(t, sk, 31923, "signed")
	invalid.Content = "tampered"
	if err := writer.Publish(ctx, invalid); err == nil {
		t.Fatal("tampered event accepted")
	}

	reader, err := nostr.RelayConnect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	sub, err := reader.Subscribe(ctx, nostr.Filter{IDs: []nostr.ID{evt.ID}}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-sub.Events:
		if got.ID != evt.ID {
			t.Fatal("wrong event read")
		}
	case <-ctx.Done():
		t.Fatal("unauthenticated reader could not retrieve event")
	}
	sub.Unsub()
	reader.Close()
	writer.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	req.Header.Set("Accept", "application/nostr+json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	err = json.NewDecoder(response.Body).Decode(&info)
	response.Body.Close()
	if err != nil || info["name"] != "BitcoinWalk staging relay" {
		t.Fatalf("NIP-11 failed: %v %v", info, err)
	}
	response, err = http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("health check failed")
	}
	server.Close()
	relay.DisableExpirationManager()
	db.Close()
	closed = true

	reopened, stored, err := newRelay(path, writers)
	if err != nil {
		t.Fatal(err)
	}
	defer stored.Close()
	defer reopened.DisableExpirationManager()
	found := false
	for event := range stored.QueryEvents(nostr.Filter{IDs: []nostr.ID{evt.ID}}, 10) {
		found = event.ID == evt.ID
	}
	if !found {
		t.Fatal("event did not survive database reopen")
	}
}
