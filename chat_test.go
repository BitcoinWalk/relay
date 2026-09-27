package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/BitcoinWalk/relay/internal/chatstate"
	"github.com/coder/websocket"
)

func TestChatWebSocketMembershipAndRevocation(t *testing.T) {
	admin, user, outsider := nostr.Generate(), nostr.Generate(), nostr.Generate()
	s, err := chatstate.OpenSigned(filepath.Join(t.TempDir(), "chat.db"), nostr.GetPublicKey(admin), nostr.Generate())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := newChatRelay(s)
	userAuthed := make(chan struct{}, 2)
	originalAuth := r.OnAuth
	r.OnAuth = func(ctx context.Context, key nostr.PubKey) {
		originalAuth(ctx, key)
		if key == nostr.GetPublicKey(user) {
			userAuthed <- struct{}{}
		}
	}
	var userAuth nostr.Event
	server := httptest.NewServer(r)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	connect := func(key nostr.SecretKey, auth bool) *nostr.Relay {
		c, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		if auth {
			probe, err := c.Subscribe(ctx, nostr.Filter{}, nostr.SubscriptionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-probe.ClosedReason:
			case <-ctx.Done():
				t.Fatal("missing auth challenge")
			}
			probe.Unsub()
			if err := c.Auth(ctx, func(_ context.Context, e *nostr.Event) error {
				err := e.Sign(key)
				if key == user {
					userAuth = *e
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		return c
	}
	owner, member, other, anon := connect(admin, true), connect(user, true), connect(outsider, true), connect(outsider, false)
	event := func(key nostr.SecretKey, kind nostr.Kind, group, content string, tags ...nostr.Tag) nostr.Event {
		e := nostr.Event{Kind: kind, CreatedAt: nostr.Now(), Content: content, Tags: nostr.Tags{{"h", group}}}
		e.Tags = append(e.Tags, tags...)
		if err := e.Sign(key); err != nil {
			t.Fatal(err)
		}
		return e
	}
	pub := func(c *nostr.Relay, e nostr.Event) {
		if err := c.Publish(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	pub(owner, event(admin, 9007, "a", "create"))
	pub(owner, event(admin, 9007, "b", "create"))
	// Armada asks for 500: discovery must work without exposing the private roster.
	discovery, err := anon.Subscribe(ctx, nostr.Filter{Kinds: []nostr.Kind{39000, 39002}, Limit: 500, Tags: nostr.TagMap{"d": []string{"a"}}}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-discovery.Events:
		if e.Kind != 39000 || !e.VerifySignature() {
			t.Fatal("invalid public discovery")
		}
	case <-ctx.Done():
		t.Fatal("missing metadata")
	}
	select {
	case <-discovery.Events:
		t.Fatal("private roster leaked")
	case <-discovery.EndOfStoredEvents:
	case <-ctx.Done():
		t.Fatal("metadata query stalled")
	}
	discovery.Unsub()
	f := nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": []string{"a"}}}
	rejected := func(c *nostr.Relay, filter nostr.Filter) {
		sub, err := c.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Unsub()
		select {
		case e := <-sub.Events:
			t.Fatalf("unauthorized event: %v", e)
		case reason := <-sub.ClosedReason:
			if reason == "" {
				t.Fatal("missing rejection")
			}
		case <-ctx.Done():
			t.Fatal("request was not rejected")
		}
	}
	rejected(anon, f)
	rejected(other, f)
	rejected(member, nostr.Filter{})
	rejected(member, nostr.Filter{IDs: []nostr.ID{}})
	rejected(member, nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": []string{"a", "b"}}})
	if err := member.Publish(ctx, event(user, 9, "a", "not yet joined")); err == nil {
		t.Fatal("nonmember wrote")
	}
	pub(member, event(user, 9021, "a", "join", nostr.Tag{"client", "Armada"}))
	status, err := member.Subscribe(ctx, nostr.Filter{Kinds: []nostr.Kind{9000, 9001}, Tags: nostr.TagMap{"h": []string{"a"}, "p": []string{nostr.GetPublicKey(user).Hex()}}, Limit: 10}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-status.Events:
		if e.Kind != 9000 || !e.VerifySignature() {
			t.Fatal("missing signed admission")
		}
	case <-ctx.Done():
		t.Fatal("membership query stalled")
	}
	status.Unsub()
	first := event(user, 9, "a", "first", nostr.Tag{"client", "Armada"})
	pub(member, first)
	pub(owner, event(admin, 9, "b", "private b history"))
	// Armada combines every discovered group, including groups not joined.
	f.Tags["h"] = []string{"b", "a"}
	ownerHistory, err := owner.Subscribe(ctx, nostr.Filter{Kinds: []nostr.Kind{9, 1068}, Tags: nostr.TagMap{"h": []string{"a"}}, Limit: 30}, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ownerHistory.Events:
		if got.ID != first.ID {
			t.Fatal("admin could not read member message")
		}
	case <-ctx.Done():
		t.Fatal("admin history missing member message")
	}
	ownerHistory.Unsub()
	sub, err := member.Subscribe(ctx, f, nostr.SubscriptionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsub()
	select {
	case got := <-sub.Events:
		if got.ID != first.ID {
			t.Fatal("wrong history")
		}
	case <-ctx.Done():
		t.Fatal("missing history")
	}
	select {
	case <-sub.EndOfStoredEvents:
	case <-ctx.Done():
		t.Fatal("missing EOSE")
	}
	// Armada may repeat AUTH as other reads/writes challenge the same socket.
	// Reauthenticating the same identity must not silently discard its live feed.
	select {
	case <-userAuthed:
	case <-ctx.Done():
		t.Fatal("initial auth callback missing")
	}
	// Bypass client Auth() caching to exercise a real repeated AUTH frame.
	authFrame, _ := json.Marshal(nostr.AuthEnvelope{Event: userAuth})
	if err := member.WriteWithError(authFrame); err != nil {
		t.Fatal(err)
	}
	select {
	case <-userAuthed:
	case <-ctx.Done():
		t.Fatal("repeat auth callback missing")
	}
	second := event(admin, 9, "a", "live")
	pub(owner, event(admin, 9, "b", "private b live"))
	pub(owner, second)
	select {
	case got := <-sub.Events:
		if got.ID != second.ID {
			t.Fatal("wrong live event")
		}
	case <-ctx.Done():
		t.Fatal("missing live event")
	}
	rejected(member, nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": []string{"b"}}})
	pub(owner, event(admin, 9001, "a", "ban", nostr.Tag{"p", nostr.GetPublicKey(user).Hex()}, nostr.Tag{"bitcoinwalk-ban", "true"}))
	pub(owner, event(admin, 9, "a", "after ban"))
	select {
	case <-sub.Events:
		t.Fatal("live message leaked after ban")
	case <-time.After(150 * time.Millisecond):
	}
	rejected(member, f)
	if err := member.Publish(ctx, event(user, 9021, "a", "rejoin")); err == nil {
		t.Fatal("banned member rejoined")
	}
	if err := member.Publish(ctx, event(user, 9, "a", "banned write")); err == nil {
		t.Fatal("banned member wrote")
	}
}

func TestArmadaTimelineFilter(t *testing.T) {
	f := nostr.Filter{Kinds: []nostr.Kind{9, 1068}, Tags: nostr.TagMap{"h": []string{"a"}}, Limit: 30}
	if !chatFilter(f) {
		t.Fatal("Armada text/poll timeline query rejected")
	}
	f.Kinds = append(f.Kinds, 30303)
	if chatFilter(f) {
		t.Fatal("application records mixed into chat query")
	}
}

func TestChatCountDisabled(t *testing.T) {
	s, err := chatstate.Open(filepath.Join(t.TempDir(), "chat.db"), nostr.GetPublicKey(nostr.Generate()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := newChatRelay(s)
	server := httptest.NewServer(r)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte(`["COUNT","probe",{}]`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var msg []json.RawMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg) < 3 || string(msg[0]) != `"CLOSED"` {
		t.Fatalf("COUNT not disabled: %s", data)
	}
}
