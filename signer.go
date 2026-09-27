package main

import (
	"context"
	"fmt"
	"net/http"
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
	r.Info.Name = "BitcoinWalk signing transport staging"
	r.Info.SupportedNIPs = []any{1, 11, 46}
	r.Info.Version = "bitcoinwalk-signer-0.1.0"
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
		fmt.Fprintln(w, `{"status":"ok","mode":"ephemeral-signer-staging"}`)
	})
	return r
}

func runSignerStaging() error {
	r := newSignerRelay()
	r.ServiceURL = "wss://chat-staging.bitcoinwalk.org/signer"
	server := &http.Server{Addr: "127.0.0.1:3336", Handler: r, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	return server.ListenAndServe()
}
