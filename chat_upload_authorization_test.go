package main

import (
	"encoding/json"
	"fiatjaf.com/nostr"
	"github.com/BitcoinWalk/relay/internal/chatstate"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uploadEvent(t *testing.T, key nostr.SecretKey, relay, group string) nostr.Event {
	t.Helper()
	content, _ := json.Marshal(chatUploadCommand{"upload-chat-image", relay, group, strings.Repeat("a", 64), "image/png", 100})
	e := nostr.Event{Kind: 27235, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"t", "bitcoinwalk-media"}, {"action", "upload-chat-image"}}, Content: string(content)}
	if err := e.Sign(key); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestChatUploadCurrentMembership(t *testing.T) {
	admin, user := nostr.Generate(), nostr.Generate()
	store, err := chatstate.OpenSigned(filepath.Join(t.TempDir(), "chat.db"), nostr.GetPublicKey(admin), nostr.Generate())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	apply := func(kind nostr.Kind, tags ...nostr.Tag) {
		e := nostr.Event{Kind: kind, CreatedAt: nostr.Now(), Tags: append(nostr.Tags{{"h", "city-a"}}, tags...)}
		if err := e.Sign(admin); err != nil {
			t.Fatal(err)
		}
		if err := store.Apply(e, e.PubKey, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	apply(9007)
	relay := newChatRelay(store)
	relay.ServiceURL = "wss://chat-staging.bitcoinwalk.org"
	registerChatUploadAuthorization(relay, store)
	event := uploadEvent(t, user, relay.ServiceURL+"/", "city-a")
	send := func(e nostr.Event, want int) {
		t.Helper()
		body, _ := json.Marshal(e)
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, httptest.NewRequest("POST", chatUploadAuthorizationPath, strings.NewReader(string(body))))
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
		}
		if want == 200 && !strings.Contains(w.Body.String(), e.ID.Hex()) {
			t.Fatal("missing event binding")
		}
	}
	send(event, 403)
	apply(9000, nostr.Tag{"p", nostr.GetPublicKey(user).Hex()})
	send(event, 200)
	send(uploadEvent(t, user, relay.ServiceURL, "other-city"), 403)
	send(uploadEvent(t, user, "wss://other.bitcoinwalk.org", "city-a"), 401)
	apply(9001, nostr.Tag{"p", nostr.GetPublicKey(user).Hex()}, nostr.Tag{"bitcoinwalk-ban", "true"})
	send(event, 403)
	store.Close()
	send(event, 403)
}

func TestChatUploadRejectsInvalidCommands(t *testing.T) {
	key := nostr.Generate()
	origin := "wss://chat-staging.bitcoinwalk.org"
	for _, test := range []struct {
		name   string
		change func(*nostr.Event)
		resign bool
	}{
		{"expired", func(e *nostr.Event) { e.CreatedAt -= 301 }, true},
		{"future", func(e *nostr.Event) { e.CreatedAt += 301 }, true},
		{"tampered", func(e *nostr.Event) { e.Content += " " }, false},
		{"wrong-kind", func(e *nostr.Event) { e.Kind = 1 }, true},
		{"duplicate-tags", func(e *nostr.Event) { e.Tags[1] = e.Tags[0] }, true},
		{"extra-field", func(e *nostr.Event) { e.Content = strings.TrimSuffix(e.Content, "}") + `,"actor":"other"}` }, true},
		{"extra-json", func(e *nostr.Event) { e.Content += "{}" }, true},
		{"encoded-path", func(e *nostr.Event) { e.Content = strings.Replace(e.Content, origin, origin+"/%2f", 1) }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := uploadEvent(t, key, origin, "city-a")
			test.change(&e)
			if test.resign {
				if err := e.Sign(key); err != nil {
					t.Fatal(err)
				}
			}
			if _, ok := parseChatUpload(e, origin, time.Now().Unix()); ok {
				t.Fatal("accepted invalid command")
			}
		})
	}
	e := uploadEvent(t, key, origin, "city-a")
	e.Tags[0], e.Tags[1] = e.Tags[1], e.Tags[0]
	e.Sign(key)
	if _, ok := parseChatUpload(e, origin, time.Now().Unix()); !ok {
		t.Fatal("tag ordering rejected")
	}
	handler := chatUploadAuthorizationHandler(origin, func(string, nostr.PubKey) error { t.Fatal("unexpected membership call"); return nil })
	for _, body := range []string{strings.Repeat("x", 16385), "{}{}"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", chatUploadAuthorizationPath, strings.NewReader(body)))
		if w.Code == 200 {
			t.Fatal("accepted malformed input")
		}
	}
}
