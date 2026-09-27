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

// Characterization regression: exercise actual multi-filter REQ frames rather
// than invoking OnRequest directly. No production relay or account is used.
func TestBundledChatQueryClosure(t *testing.T) {
	for _, mode := range []string{"chat-only", "chat-then-profile", "profile-then-chat", "separate-subscriptions"} {
		t.Run(mode, func(t *testing.T) {
			admin, user := nostr.Generate(), nostr.Generate()
			store, err := chatstate.OpenSigned(filepath.Join(t.TempDir(), "chat.db"), nostr.GetPublicKey(admin), nostr.Generate())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			event := func(key nostr.SecretKey, kind nostr.Kind, content string) nostr.Event {
				e := nostr.Event{Kind: kind, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"h", "a"}}, Content: content}
				if err := e.Sign(key); err != nil {
					t.Fatal(err)
				}
				return e
			}
			for _, e := range []nostr.Event{event(admin, 9007, "create"), event(user, 9021, "join")} {
				if err := store.Apply(e, e.PubKey, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(newChatRelay(store))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			url := "ws" + strings.TrimPrefix(server.URL, "http")
			ws, _, err := websocket.Dial(ctx, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.CloseNow()
			send := func(v any) {
				t.Helper()
				data, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				if err = ws.Write(ctx, websocket.MessageText, data); err != nil {
					t.Fatal(err)
				}
			}
			read := func() []json.RawMessage {
				t.Helper()
				_, data, err := ws.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var m []json.RawMessage
				if err = json.Unmarshal(data, &m); err != nil {
					t.Fatal(err)
				}
				return m
			}
			value := func(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
			send([]any{"REQ", "auth-probe", map[string]any{}})
			for {
				m := read()
				if value(m[0]) == "AUTH" {
					auth := nostr.Event{Kind: 22242, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"relay", url}, {"challenge", value(m[1])}}}
					if err := auth.Sign(user); err != nil {
						t.Fatal(err)
					}
					send([]any{"AUTH", auth})
					break
				}
			}
			for {
				m := read()
				if value(m[0]) == "OK" {
					if string(m[2]) != "true" {
						t.Fatalf("auth rejected: %s", m)
					}
					break
				}
			}
			chat := map[string]any{"kinds": []int{9}, "#h": []string{"a"}, "limit": 30}
			profile := map[string]any{"kinds": []int{0}, "authors": []string{nostr.GetPublicKey(user).Hex()}}
			switch mode {
			case "chat-only", "separate-subscriptions":
				send([]any{"REQ", "feed", chat})
			case "chat-then-profile":
				send([]any{"REQ", "feed", chat, profile})
			case "profile-then-chat":
				send([]any{"REQ", "feed", profile, chat})
			}
			mixed := mode == "chat-then-profile" || mode == "profile-then-chat"
			for {
				m := read()
				if len(m) < 2 || value(m[1]) != "feed" {
					continue
				}
				kind := value(m[0])
				if kind == "EOSE" || kind == "CLOSED" {
					if mixed && kind != "CLOSED" {
						t.Fatal("expected entire mixed subscription to close")
					}
					if !mixed && kind != "EOSE" {
						t.Fatalf("chat-only subscription rejected: %s", m)
					}
					t.Logf("%s: %s", mode, kind)
					break
				}
			}
			if mixed {
				return
			} // CLOSED is terminal for a conforming client.
			if mode == "separate-subscriptions" {
				send([]any{"REQ", "profile", profile})
				for {
					m := read()
					if value(m[0]) == "CLOSED" && value(m[1]) == "profile" {
						break
					}
					if value(m[0]) == "CLOSED" && value(m[1]) == "feed" {
						t.Fatal("unrelated rejection closed feed")
					}
				}
			}
			message := event(user, 9, "local live control")
			send([]any{"EVENT", message})
			ack, live := false, false
			for !ack || !live {
				m := read()
				switch value(m[0]) {
				case "OK":
					if value(m[1]) == message.ID.Hex() {
						if string(m[2]) != "true" {
							t.Fatal("message rejected")
						}
						ack = true
					}
				case "EVENT":
					if value(m[1]) == "feed" {
						var e nostr.Event
						if err := json.Unmarshal(m[2], &e); err != nil {
							t.Fatal(err)
						}
						if e.ID != message.ID {
							t.Fatal("unexpected message")
						}
						live = true
					}
				case "CLOSED":
					if value(m[1]) == "feed" {
						t.Fatal("live feed closed")
					}
				}
			}
			t.Log("message acknowledged and delivered live without reconnect")
		})
	}
}
