package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
)

// A separate ephemeral transport: no identity key, database or chat access.
// Recipient filters are routing, NOT authorization. Anyone knowing a recipient
// pubkey can observe its encrypted envelopes. Only the endpoints decrypt them.
func newSignerRelay() *khatru.Relay {
	r := khatru.NewRelay()
	r.Info.Name = env("RELAY_SIGNER_NAME", "BitcoinWalk remote signing transport")
	r.Info.Description = "Ephemeral NIP-46 transport. Encrypted signing messages are relayed live and never stored."
	r.Info.SupportedNIPs = []any{1, 11, 46}
	r.Info.Version = "bitcoinwalk-remote-signer-0.8.61"
	r.MaxMessageSize = 65536
	r.WriteWait = 2 * time.Second
	r.Negentropy = false
	limits := &chatLimits{}
	var mu sync.Mutex
	counts := map[*khatru.WebSocket]int{}
	type lease struct {
		at     time.Time
		active bool
	}
	connections := map[*http.Request]lease{}
	r.OnConnect = func(ctx context.Context) {
		mu.Lock()
		connections[khatru.GetConnection(ctx).Request] = lease{at: time.Now(), active: true}
		mu.Unlock()
	}
	r.OnListenerRemoved = func(ws *khatru.WebSocket, _ int, _ string, _ nostr.Filter) {
		mu.Lock()
		if counts[ws] > 0 {
			counts[ws]--
		}
		mu.Unlock()
	}
	r.OnDisconnect = func(ctx context.Context) {
		mu.Lock()
		delete(counts, khatru.GetConnection(ctx))
		delete(connections, khatru.GetConnection(ctx).Request)
		mu.Unlock()
	}
	r.RejectConnection = func(req *http.Request) bool {
		if !limits.allow("connect:"+khatru.GetIPFromRequest(req), 30, time.Minute, time.Now()) {
			return true
		}
		mu.Lock()
		defer mu.Unlock()
		for request, l := range connections {
			if !l.active && time.Since(l.at) > time.Minute {
				delete(connections, request)
			}
		}
		if len(connections) >= 128 {
			return true
		}
		connections[req] = lease{at: time.Now()}
		return false
	}
	r.OnEvent = func(ctx context.Context, e nostr.Event) (bool, string) {
		if e.Kind != 24133 {
			return true, "restricted: signing messages only"
		}
		if len(e.Content) == 0 || len(e.Content) > 32768 || len(e.Tags) != 1 || len(e.Tags[0]) != 2 || e.Tags[0][0] != "p" {
			return true, "invalid: one recipient and bounded content required"
		}
		if _, err := nostr.PubKeyFromHex(e.Tags[0][1]); err != nil {
			return true, "invalid: recipient"
		}
		now := nostr.Now()
		if e.CreatedAt < now-300 || e.CreatedAt > now+60 {
			return true, "invalid: timestamp"
		}
		if !e.CheckID() || !e.VerifySignature() {
			return true, "invalid: signature"
		}
		if !limits.allow("all-write", 1200, time.Minute, time.Now()) || !limits.allow("write:"+khatru.GetIP(ctx), 240, time.Minute, time.Now()) {
			return true, "rate-limited: signer transport"
		}
		return false, ""
	}
	r.OnRequest = func(ctx context.Context, f nostr.Filter) (bool, string) {
		if len(f.Kinds) != 1 || f.Kinds[0] != 24133 || len(f.Tags) != 1 || len(f.Tags["p"]) != 1 {
			return true, "restricted: one signing recipient required"
		}
		if _, err := nostr.PubKeyFromHex(f.Tags["p"][0]); err != nil {
			return true, "invalid: recipient"
		}
		mu.Lock()
		defer mu.Unlock()
		n := counts[khatru.GetConnection(ctx)]
		if n >= 8 || !limits.allow("query:"+khatru.GetIP(ctx), 240, time.Minute, time.Now()) {
			return true, "rate-limited: subscriptions"
		}
		counts[khatru.GetConnection(ctx)]++
		return false, ""
	}
	r.Router().HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `{"status":"ok","mode":"ephemeral-remote-signer"}`)
	})
	return r
}

func runSignerTransport(mode string) error {
	if mode != "staging" && mode != "remote" {
		return errors.New("RELAY_SIGNER_MODE must be staging or remote")
	}
	listen := "127.0.0.1:3336"
	serviceURL := "wss://chat-staging.bitcoinwalk.org/signer"
	if mode == "remote" {
		listen = "127.0.0.1:3344"
		serviceURL = "wss://remote.bitcoinwalk.org/"
	}
	listen = env("RELAY_SIGNER_LISTEN", listen)
	if err := validateListenAddress(listen, false); err != nil {
		return err
	}
	r := newSignerRelay()
	r.ServiceURL = env("RELAY_SIGNER_SERVICE_URL", serviceURL)
	server := &http.Server{Addr: listen, Handler: r, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	return server.ListenAndServe()
}

func runSignerAcceptance(target string) error {
	if !strings.HasPrefix(target, "wss://") {
		return errors.New("signer acceptance requires a wss URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sender, err := nostr.RelayConnect(ctx, target, nostr.RelayOptions{})
	if err != nil {
		return fmt.Errorf("connect sender: %w", err)
	}
	defer sender.Close()
	receiver, err := nostr.RelayConnect(ctx, target, nostr.RelayOptions{})
	if err != nil {
		return fmt.Errorf("connect receiver: %w", err)
	}
	defer receiver.Close()
	senderKey := nostr.Generate()
	recipient := nostr.GetPublicKey(nostr.Generate()).Hex()
	filter := nostr.Filter{Kinds: []nostr.Kind{24133}, Tags: nostr.TagMap{"p": []string{recipient}}}
	sub, err := receiver.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Unsub()
	select {
	case <-sub.EndOfStoredEvents:
	case <-ctx.Done():
		return errors.New("signer acceptance did not receive EOSE")
	}
	event := nostr.Event{Kind: 24133, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", recipient}}, Content: "bitcoinwalk remote signer acceptance"}
	event.Sign(senderKey)
	if err := sender.Publish(ctx, event); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	select {
	case got := <-sub.Events:
		if got.ID != event.ID {
			return errors.New("signer acceptance received the wrong live event")
		}
	case <-ctx.Done():
		return errors.New("signer acceptance live delivery timed out")
	}
	late, err := receiver.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		return fmt.Errorf("late subscribe: %w", err)
	}
	defer late.Unsub()
	select {
	case <-late.Events:
		return errors.New("signer acceptance found persisted history")
	case <-late.EndOfStoredEvents:
	case <-ctx.Done():
		return errors.New("signer acceptance history check timed out")
	}
	fmt.Printf("ephemeral NIP-46 delivery accepted at %s\n", target)
	return nil
}
