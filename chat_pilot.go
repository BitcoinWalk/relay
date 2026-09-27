package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"github.com/BitcoinWalk/relay/internal/chatstate"
)

// Local-only disposable pilot: no user keys, real city data, or production
// settings. Signing keys live in memory and are intentionally not persisted.
func runChatPilot() error {
	dir, err := os.MkdirTemp("", "bitcoinwalk-chat-pilot-")
	if err != nil {
		return err
	}
	admin, relayKey := nostr.Generate(), nostr.Generate()
	s, err := chatstate.OpenSigned(dir+"/chat.db", nostr.GetPublicKey(admin), relayKey)
	if err != nil {
		return err
	}
	defer s.Close()
	for _, id := range []string{"global-pilot", "city-pilot"} {
		e := nostr.Event{Kind: 9007, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"h", id}}, Content: "Disposable BitcoinWalk chat test"}
		if err := e.Sign(admin); err != nil {
			return err
		}
		if err := s.Apply(e, e.PubKey, time.Now()); err != nil {
			return err
		}
	}
	r := newChatRelay(s)
	r.ServiceURL = "ws://localhost:3342"
	registerChooserAssets(r.Router())
	for route, group := range map[string]string{"/join-chat": "global-pilot", "/city-test/join-chat": "city-pilot"} {
		target := "http://localhost:3343/s/" + url.QueryEscape("ws:localhost:3342") + "/" + url.PathEscape(group)
		title := "Global BitcoinWalk chat"
		if group == "city-pilot" {
			title = "BitcoinWalk city test chat"
		}
		registerChatChooser(r.Router(), route, chatDestination{title, target, true})
	}
	r.Router().HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `{"status":"ok","mode":"disposable-local-chat"}`)
	})
	server := &http.Server{Addr: "127.0.0.1:3342", Handler: r, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Print("Disposable local chat: http://localhost:3342/join-chat (requires Armada on port 3343). Restart resets groups and identities; do not use real/private content.")
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
